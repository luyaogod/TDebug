package debug

// 接口日志(wsfa_t)查询与报文读取。
// 机制来自对 T100 的调研(awsp900_01.insertInto/writeResponseLog):
//   wsfa001=服务名 wsfa003/004=起止时间 wsfa005=耗时 wsfa006=返回码
//   wsfa007/008=请求/响应报文文件路径($TEMPDIR/日期/ws_req|res_时间_GUID.xml)
//   wsfa010/011=报文全文 CLOB(超过 A-SYS-0077 KB 上限时不入库,只记大小 wsfa016/017)
//   wsfa012=作业编号(gzja_t 服务名→程序解析结果) wsfa014=错误描述(≤100字)
// 列集对齐 T100 原生页 awsq990.4gl(整合服務端檢測工具):它 select 出
//   wsfa001,005,006,004,002,003,012,013,014,015,018,页面显示 9 列。
// 本工具此前只取其中一部分,现补齐原生页有而我们缺的四项:
//   wsfa002=服务程序序号(process_id) wsfa013=发起端 wsfa018=服务端 wsfa015=sso秒数
// (字段中文名以 tdict rt wsfa_t 的字典为准;awsa990 的列名与之一致)
// 重放调试 = 日志里内嵌的 `r.dg <作业> '<req>' '<rsp>'`:报文文件作 argv 重跑服务程序。

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"tdebug/host"
	"time"

	"github.com/pkg/sftp"
)

// WSLogItem 接口日志列表行
type WSLogItem struct {
	RowID    string `json:"rowid"`
	Service  string `json:"service"`  // wsfa001 服务名称
	PID      string `json:"pid"`      // wsfa002 process_id(服务程序序号)
	Start    string `json:"start"`    // wsfa003 起始时间
	End      string `json:"end"`      // wsfa004 结束时间
	Duration string `json:"duration"` // wsfa005 处理时间
	Code     string `json:"code"`     // wsfa006 状态(srvcode,000=成功)
	Job      string `json:"job"`      // wsfa012 服务程序编号(重放时的作业)
	ReqPath  string `json:"reqPath"`  // wsfa007
	RspPath  string `json:"rspPath"`  // wsfa008
	ReqSize  string `json:"reqSize"`  // wsfa016(字节)
	RspSize  string `json:"rspSize"`  // wsfa017
	ErrMsg   string `json:"errMsg"`   // wsfa014 错误消息
	Origin   string `json:"origin"`   // wsfa013 发起端
	Server   string `json:"server"`   // wsfa018 服务端
	SSO      string `json:"sso"`      // wsfa015 sso秒数(小数 6 位)
}

// WSLogContent 选中日志的报文内容
type WSLogContent struct {
	Request  string `json:"request"`
	Response string `json:"response"`
	// RequestPartial 请求报文内容不完整(超过读取上限被截断,或源文件已清理只剩入库前 2000 字符)。
	// 按原文件重放不受影响,但**基于该内容编辑后重放会送出残缺报文**,故界面据此禁用"用修改后的入参重放"。
	RequestPartial bool `json:"requestPartial,omitempty"`
}

// reRowid 行标识白名单(拼 SQL 防注入):Oracle rowid / 金仓 ctid「(页,元组)」
var reRowid = regexp.MustCompile(`^(\([0-9]+,[0-9]+\)|[A-Za-z0-9./]{1,20})$`)

// reWSLogRow 列表行解析:字段间以 | 分隔;ErrMsg(第 10 列)允许空格——
// 失败记录的错误描述如「[T100_message] 处理笔数 1, 成功 0, 失败 1」含空格,
// 用 \S+ 会整行匹配失败导致记录被静默丢弃。
// 末尾五列(rspSize/发起端/服务端/sso/结束时间)用 [^|]* 而不是 .* —— 它们不含 |,
// 收紧后贪婪的 (.*) 不会把尾巴吃空。
var reWSLogRow = regexp.MustCompile(`^(\S+)\|(\S*)\|(\S*)\|(.*)\|(.*)\|(\S*)\|(\S*)\|(\S*)\|(.*)\|(.*)\|(\S*)\|([^|]*)\|([^|]*)\|([^|]*)\|([^|]*)\|([^|]*)$`)

// sqlWSLogCols 列表/详情共用的选列片段(顺序即 reWSLogRow 的分组顺序)。
// 末四项是对齐 T100 原生页 awsq990 补上的:wsfa013 发起端 / wsfa018 服务端 /
// wsfa015 sso秒数(sso 固定 6 位小数,与原生页显示一致;Oracle 需显式 format,
// 金仓 numeric::text 本身就保留标度)/ wsfa004 结束时间(截到秒,与 start 一致)。
// 所有列都套 nvl/coalesce:|| 遇 NULL 会把整行拼成 NULL(此前只有数值列这么处理)。
func sqlWSLogCols(kingbase bool) string {
	if kingbase {
		return `coalesce(wsfa013,'')||'|'||coalesce(wsfa018,'')||'|'||coalesce(wsfa015::text,'')||'|'||coalesce(substr(wsfa004,1,19),'')`
	}
	return `nvl(wsfa013,'')||'|'||nvl(wsfa018,'')||'|'||nvl(to_char(wsfa015,'FM99999999999990.000000'),'')||'|'||substr(nvl(wsfa004,''),1,19)`
}

// WSLogFilter 列表过滤条件。口径对齐 awsq990 主查询:
//
//	基线永远排除 wsfa001='docno.storage'(SSO 记录:wsfa003 存的不是时间,会刷满整页)
//	wsfa001 服务名(= / in,本工具放宽为 LIKE 通配)、wsfa006 处理结果、wsfa013 发起端、wsfa018 服务端
//	时间窗:wsfa003 >= 起 且 wsfa004 <= 止(整个调用落在窗口内)
//	onlyFail 为本工具扩展
type WSLogFilter struct {
	Service   string // 服务名(wsfa001),支持 * ? 通配
	Job       string // 作业编号(wsfa012),支持 * ? 通配 —— 按"哪个作业"找日志的入口
	Result    string // 处理结果(wsfa006)等值
	Origin    string // 发起端(wsfa013)等值
	Server    string // 服务端(wsfa018)等值
	PID       string // 服务程序序号(wsfa002)等值 —— 界面「服务程序」列就是它
	OnlyFail  bool   // 仅失败(wsfa006<>'000')
	StartFrom string // 起始时间起(wsfa003 >=,字符串字典序即时间序)
	EndTo     string // 结束时间止(wsfa004 <=;纯日期补到当天 23:59:59.99999)
	Page      int    // 从 1 起
	PageSize  int    // 每页条数(50..500,默认 200)
}

// reQBEValue 时间类输入白名单(仅日期时间字符)
var reQBEValue = regexp.MustCompile(`^[0-9: -]{0,19}$`)

// reWSLogEq 等值条件输入白名单:只禁单引号。
// 单引号是唯一的字符串逃逸手段(Oracle 与金仓都不把 \ 当转义),禁掉即可安全内联;
// 其余字符(空格/中文等)一律放行,避免过严导致"填了却静默查不到"。
var reWSLogEq = regexp.MustCompile(`^[^']{1,40}$`)

// wslogWhere 拼 WHERE 子句(纯函数,便于单测)。
// 条件全部为 AND,顺序固定;时间格式非法才返回 error(与既有接口行为一致)。
func wslogWhere(f WSLogFilter) (string, error) {
	// 基线:原生页固定排除的噪声服务(SSO 记录占满整页,见类型注释)
	wc := "1=1 AND wsfa001 != 'docno.storage'"
	if f.Service != "" {
		// 服务名(如 icd.erp.wo.out.query.get)支持 * ? 通配;其余字符白名单校验。
		// 这是原生 `wsfa001 = '值'` / `in (…)` 的超集:填精确值(不带通配符)时结果一致。
		patOK := regexp.MustCompile(`^[A-Za-z0-9._*?]{1,60}$`)
		if !patOK.MatchString(f.Service) {
			wc += " AND 1=0"
		} else {
			pat := strings.ReplaceAll(strings.ReplaceAll(strings.ToUpper(f.Service), "*", "%"), "?", "_")
			wc += fmt.Sprintf(" AND UPPER(wsfa001) LIKE '%s'", strings.ReplaceAll(pat, "'", "''"))
		}
	}
	if f.Job != "" {
		// 作业编号(wsfa012,如 wssp01131):与 service 同款通配。
		// 这是**按"哪个作业"找日志**的唯一入口 —— 服务名是 oa.schema.data.get 这类反域名,
		// 光看服务名根本认不出是哪个作业在跑。
		patOK := regexp.MustCompile(`^[A-Za-z0-9._*?]{1,60}$`)
		if !patOK.MatchString(f.Job) {
			wc += " AND 1=0"
		} else {
			pat := strings.ReplaceAll(strings.ReplaceAll(strings.ToUpper(f.Job), "*", "%"), "?", "_")
			wc += fmt.Sprintf(" AND UPPER(wsfa012) LIKE '%s'", strings.ReplaceAll(pat, "'", "''"))
		}
	}
	// 四个等值条件:处理结果 / 发起端 / 服务端 / 服务程序序号(原生 `and wsfa006 = '…'` 等)
	// wsfa002 是数字列,内联成 '861637' 由数据库隐式转换,与其它等值条件同一个白名单。
	for _, eq := range []struct{ val, col string }{
		{f.Result, "wsfa006"}, {f.Origin, "wsfa013"}, {f.Server, "wsfa018"}, {f.PID, "wsfa002"},
	} {
		v := strings.TrimSpace(eq.val)
		if v == "" {
			continue
		}
		if !reWSLogEq.MatchString(v) {
			wc += " AND 1=0"
			continue
		}
		wc += fmt.Sprintf(" AND %s = '%s'", eq.col, v)
	}
	if f.OnlyFail {
		wc += " AND wsfa006 <> '000'"
	}
	// 时间窗(原生口径):下界看起始时间 wsfa003,上界看结束时间 wsfa004
	if v := strings.TrimSpace(f.StartFrom); v != "" {
		if !reQBEValue.MatchString(v) {
			return "", fmt.Errorf("时间条件格式非法: %q", v)
		}
		wc += fmt.Sprintf(" AND wsfa003 >= '%s'", v)
	}
	if v := strings.TrimSpace(f.EndTo); v != "" {
		if !reQBEValue.MatchString(v) {
			return "", fmt.Errorf("时间条件格式非法: %q", v)
		}
		// 纯日期(yyyy-mm-dd)作上界时补到当天末尾(含毫秒位,与原生一致),
		// 否则字典序会把当天最后不到 1 秒的记录挡掉
		if len(v) == 10 {
			v += " 23:59:59.99999"
		}
		wc += fmt.Sprintf(" AND wsfa004 <= '%s'", v)
	}
	return wc, nil
}

// listWSLogs 查询接口日志列表(Oracle: rowid + OFFSET/FETCH;金仓: ctid + OFFSET/LIMIT)
func listWSLogs(conn *host.SSHConn, dbc *dbRun, f WSLogFilter) (items []WSLogItem, hasMore bool, err error) {
	size := f.PageSize
	if size <= 0 || size > 500 {
		size = 200
	}
	page := f.Page
	if page < 1 {
		page = 1
	}
	wc, err := wslogWhere(f)
	if err != nil {
		return nil, false, err
	}
	connStr, err := dbc.dbConnStr("ds")
	if err != nil {
		return nil, false, err
	}
	var out string
	if dbc.conn.Type == "kingbase" {
		// 金仓:ctid 作行标识;|| 遇 null 归 null,逐列 coalesce
		sql := fmt.Sprintf(`select wsfa.ctid||'|'||wsfa001||'|'||wsfa002||'|'||coalesce(substr(wsfa003,1,19),'')||'|'||coalesce(wsfa005::text,'')||'|'||coalesce(wsfa006,'')||'|'||coalesce(wsfa012,'')||'|'||coalesce(wsfa007,'')||'|'||coalesce(wsfa008,'')||'|'||coalesce(wsfa014,'')||'|'||coalesce(wsfa016::text,'')||'|'||coalesce(wsfa017::text,'')||'|'||%s from wsfa_t wsfa where %s order by wsfa003 desc, wsfa004 desc offset %d limit %d`, sqlWSLogCols(true), wc, (page-1)*size, size+1)
		out, err = dbc.exec(conn, connStr, "", sql, 40*time.Second)
	} else {
		sql := fmt.Sprintf(`set heading off
set feedback off
set trimspool on
set linesize 32767
select wsfa.rowid||'|'||wsfa001||'|'||wsfa002||'|'||substr(nvl(wsfa003,''),1,19)||'|'||nvl(wsfa005,'')||'|'||nvl(wsfa006,'')||'|'||nvl(wsfa012,'')||'|'||nvl(wsfa007,'')||'|'||nvl(wsfa008,'')||'|'||nvl(wsfa014,'')||'|'||nvl(wsfa016,'')||'|'||nvl(wsfa017,'')||'|'||%s
from wsfa_t wsfa where %s order by wsfa003 desc, wsfa004 desc offset %d rows fetch first %d rows only;`, sqlWSLogCols(false), wc, (page-1)*size, size+1)
		out, err = dbc.exec(conn, connStr, sql, "", 40*time.Second)
	}
	if err != nil {
		return nil, false, fmt.Errorf("查询 wsfa_t 失败: %w (%s)", err, host.FirstLines(out, 3))
	}
	var all []WSLogItem
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "ORA-") {
			return nil, false, fmt.Errorf("wsfa_t 查询出错: %s", host.FirstLines(ln, 2))
		}
		m := reWSLogRow.FindStringSubmatch(strings.TrimRight(ln, " \r"))
		if m == nil {
			continue
		}
		all = append(all, WSLogItem{
			RowID: m[1], Service: m[2], PID: m[3], Start: m[4], Duration: m[5],
			Code: m[6], Job: m[7], ReqPath: m[8], RspPath: m[9],
			ErrMsg: m[10], ReqSize: m[11], RspSize: m[12],
			Origin: m[13], Server: m[14], SSO: m[15], End: m[16],
		})
	}
	if len(all) > size {
		hasMore = true
		all = all[:size]
	}
	return all, hasMore, nil
}

// WSLogDetail 取单条日志:列表字段 + 报文内容(CLOB 优先,文件回退)
func WSLogDetail(conn *host.SSHConn, dbc *dbRun, rowid string) (*WSLogItem, *WSLogContent, error) {
	if !reRowid.MatchString(rowid) {
		return nil, nil, fmt.Errorf("rowid 格式非法")
	}
	var out string
	var err error
	connStr, err := dbc.dbConnStr("ds")
	if err != nil {
		return nil, nil, err
	}
	if dbc.conn.Type == "kingbase" {
		sql := fmt.Sprintf(`select wsfa.ctid||'|'||wsfa001||'|'||wsfa002||'|'||coalesce(substr(wsfa003,1,19),'')||'|'||coalesce(wsfa005::text,'')||'|'||coalesce(wsfa006,'')||'|'||coalesce(wsfa012,'')||'|'||coalesce(wsfa007,'')||'|'||coalesce(wsfa008,'')||'|'||coalesce(wsfa014,'')||'|'||coalesce(wsfa016::text,'')||'|'||coalesce(wsfa017::text,'')||'|'||%s from wsfa_t wsfa where ctid='%s'`, sqlWSLogCols(true), rowid)
		out, err = dbc.exec(conn, connStr, "", sql, 30*time.Second)
	} else {
		sql := fmt.Sprintf(`set heading off
set feedback off
set trimspool on
set linesize 32767
set long 300000
set longchunksize 100000
select wsfa.rowid||'|'||wsfa001||'|'||wsfa002||'|'||substr(nvl(wsfa003,''),1,19)||'|'||nvl(wsfa005,'')||'|'||nvl(wsfa006,'')||'|'||nvl(wsfa012,'')||'|'||nvl(wsfa007,'')||'|'||nvl(wsfa008,'')||'|'||nvl(wsfa014,'')||'|'||nvl(wsfa016,'')||'|'||nvl(wsfa017,'')||'|'||%s
from wsfa_t wsfa where rowid='%s';`, sqlWSLogCols(false), rowid)
		out, err = dbc.exec(conn, connStr, sql, "", 30*time.Second)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("查询 wsfa_t 失败: %w (%s)", err, host.FirstLines(out, 3))
	}
	var item *WSLogItem
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "ORA-") {
			return nil, nil, fmt.Errorf("wsfa_t 查询出错: %s", host.FirstLines(ln, 2))
		}
		if m := reWSLogRow.FindStringSubmatch(strings.TrimRight(ln, " \r")); m != nil {
			item = &WSLogItem{
				RowID: m[1], Service: m[2], PID: m[3], Start: m[4], Duration: m[5],
				Code: m[6], Job: m[7], ReqPath: m[8], RspPath: m[9],
				ErrMsg: m[10], ReqSize: m[11], RspSize: m[12],
				Origin: m[13], Server: m[14], SSO: m[15], End: m[16],
			}
			break
		}
	}
	if item == nil {
		return nil, nil, fmt.Errorf("日志记录不存在(可能已被清理)")
	}

	content := &WSLogContent{}
	// 1) 优先读报文文件(磁盘,最完整;按日期目录轮转清理,旧文件可能不存在)
	//    两个文件共用一个 SFTP 通道并发读:此前是"每文件各开一次 SFTP 子通道 + 串行",
	//    每次都要一次子系统握手;pkg/sftp 的 Client 明确支持多 goroutine 并发(内部请求 id 多路复用),
	//    但不可与 Close 并发,故先 Wait 再 Close。
	//    返回值第二个布尔 = 是否被 262144 上限截断(界面要据此禁用"改后重放")。
	readFile := func(s *sftp.Client, path string) (string, bool) {
		if path == "" {
			return "", false
		}
		f, e := s.Open(path)
		if e != nil {
			return "", false
		}
		defer f.Close()
		var size int64 = -1
		if st, e := f.Stat(); e == nil {
			size = st.Size()
		}
		buf := make([]byte, 262144)
		n, _ := f.Read(buf) // File.Read 内部会循环填满缓冲区(或到 EOF),无需自己重试
		return string(buf[:n]), size > int64(n)
	}
	var reqTrunc, rspTrunc bool
	if item.ReqPath != "" || item.RspPath != "" {
		if s, e := conn.SFTP(); e == nil {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); content.Request, reqTrunc = readFile(s, item.ReqPath) }()
			go func() { defer wg.Done(); content.Response, rspTrunc = readFile(s, item.RspPath) }()
			wg.Wait()
			s.Close()
		}
	}
	_ = rspTrunc

	// 2) 文件已被清理 → 回退 CLOB(Oracle 用 dbms_lob.substr 分段规避 ORA-06502;
	//    金仓 text 列直接 substr,ksql 原样输出)
	if content.Request == "" || content.Response == "" {
		var clobOut string
		if dbc.conn.Type == "kingbase" {
			sql := fmt.Sprintf(`select '<<<REQ>>>' from wsfa_t where ctid='%s' union all select coalesce(substr(wsfa010,1,2000),' ') from wsfa_t where ctid='%s' union all select '<<<RSP>>>' from wsfa_t where ctid='%s' union all select coalesce(substr(wsfa011,1,2000),' ') from wsfa_t where ctid='%s'`, rowid, rowid, rowid, rowid)
			clobOut, _ = dbc.exec(conn, connStr, "", sql, 60*time.Second)
		} else {
			clobSQL := fmt.Sprintf(`set heading off
set feedback off
set trimspool on
set linesize 32767
set long 30000
set longchunksize 10000
select '<<<REQ>>>' from wsfa_t where rowid='%s';
select dbms_lob.substr(wsfa010,2000,1) from wsfa_t where rowid='%s' and wsfa010 is not null;
select '<<<RSP>>>' from wsfa_t where rowid='%s';
select dbms_lob.substr(wsfa011,2000,1) from wsfa_t where rowid='%s' and wsfa011 is not null;`,
				rowid, rowid, rowid, rowid)
			clobOut, _ = dbc.exec(conn, connStr, clobSQL, "", 60*time.Second)
		}
		sec := ""
		var reqLines, rspLines []string
		for _, ln := range strings.Split(clobOut, "\n") {
			ln = strings.TrimRight(ln, " \r")
			switch strings.TrimSpace(ln) {
			case "<<<REQ>>>":
				sec = "req"
				continue
			case "<<<RSP>>>":
				sec = "rsp"
				continue
			}
			if ln == "" || strings.HasPrefix(ln, "GLOBAL_NAME") || strings.Contains(ln, "rows selected") || strings.Contains(ln, "row selected") || strings.Contains(ln, "SQL>") || strings.Contains(ln, "PL/SQL") ||
				strings.Contains(ln, "dbms_lob") || strings.HasPrefix(ln, "select ") || strings.HasPrefix(ln, "ERROR at line") || strings.Contains(ln, "ORA-") {
				continue // sqlplus 会把 stdin 的 SQL 回显出来,一并过滤
			}
			switch sec {
			case "req":
				reqLines = append(reqLines, ln)
			case "rsp":
				rspLines = append(rspLines, ln)
			}
		}
		if content.Request == "" && len(reqLines) > 0 {
			content.Request = strings.Join(reqLines, "\n") + "\n(仅显示前 2000 字符,源文件已被清理)"
			reqTrunc = true // CLOB 回退只取前 2000 字符:内容本身就是残缺的
		}
		if content.Response == "" && len(rspLines) > 0 {
			content.Response = strings.Join(rspLines, "\n") + "\n(仅显示前 2000 字符,源文件已被清理)"
		}
	}

	if content.Request == "" && content.Response == "" {
		content.Request = "(报文已被清理:超过入库大小上限且源文件已不存在)"
		reqTrunc = true // 占位文本不是可用入参
	}
	content.RequestPartial = reqTrunc
	return item, content, nil
}

// maxReplayPayload 界面回传的入参上限(与接口测试同一量级;防止误把大文件塞进 argv 文件)
const maxReplayPayload = 256 * 1024

// checkReplayOverride 校验界面回传的"改过的入参":空表示用原报文;超限直接拒绝
func checkReplayOverride(s string) error {
	if s == "" {
		return nil
	}
	if len(s) > maxReplayPayload {
		return fmt.Errorf("入参 %d 字节,超过 %dKB 上限,拒绝重放", len(s), maxReplayPayload/1024)
	}
	return nil
}

// WriteReplayFiles 报文文件不存在时,把 CLOB 内容写到服务器临时文件供重放。
// 返回可用的 (reqPath, rspPath, error);路径来自日志记录或新生成的临时文件。
// reqOverride 非空 = 界面里改过的入参:无条件落临时文件并优先使用
// (**不能走 ensure 的"原文件还在就用原文件"分支**,否则用户的修改会被静默忽略)。
func WriteReplayFiles(conn *host.SSHConn, item *WSLogItem, content *WSLogContent, reqOverride string) (reqPath, rspPath string, err error) {
	if err := checkReplayOverride(reqOverride); err != nil {
		return "", "", err
	}
	sftp, err := conn.SFTP()
	if err != nil {
		return "", "", err
	}
	defer sftp.Close()
	// 内容落 $TEMPDIR(aws 报文同目录规则)
	writeTemp := func(text string) (string, error) {
		out, e := conn.Output("bash -lc 'echo $TEMPDIR'", 10*time.Second)
		tmp := strings.TrimSpace(out)
		if e != nil || tmp == "" {
			tmp = "/tmp"
		}
		np := fmt.Sprintf("%s/tdebug_replay_%d.xml", tmp, time.Now().UnixNano()%1000000)
		nf, e := sftp.Create(np)
		if e != nil {
			return "", fmt.Errorf("写重放报文失败: %w", e)
		}
		defer nf.Close()
		if _, e := nf.Write([]byte(text)); e != nil {
			return "", fmt.Errorf("写重放报文失败: %w", e)
		}
		return np, nil
	}
	ensure := func(path, text string) (string, error) {
		if path != "" {
			if f, e := sftp.Open(path); e == nil {
				f.Close()
				return path, nil // 文件还在
			}
		}
		if text == "" {
			return "", nil
		}
		return writeTemp(text)
	}
	if reqOverride != "" {
		if reqPath, err = writeTemp(reqOverride); err != nil {
			return "", "", err
		}
	} else if reqPath, err = ensure(item.ReqPath, content.Request); err != nil {
		return "", "", err
	}
	if rspPath, err = ensure(item.RspPath, content.Response); err != nil {
		return "", "", err
	}
	if reqPath == "" {
		return "", "", fmt.Errorf("请求报文不可用(文件已清理且未入库)")
	}
	return reqPath, rspPath, nil
}
