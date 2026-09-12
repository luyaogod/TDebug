package debug

// 接口日志(wsfa_t)查询与报文读取。
// 机制来自对 T100 的调研(awsp900_01.insertInto/writeResponseLog):
//   wsfa001=服务名 wsfa003/004=起止时间 wsfa005=耗时 wsfa006=返回码
//   wsfa007/008=请求/响应报文文件路径($TEMPDIR/日期/ws_req|res_时间_GUID.xml)
//   wsfa010/011=报文全文 CLOB(超过 A-SYS-0077 KB 上限时不入库,只记大小 wsfa016/017)
//   wsfa012=作业编号(gzja_t 服务名→程序解析结果) wsfa014=错误描述(≤100字)
// 重放调试 = 日志里内嵌的 `r.dg <作业> '<req>' '<rsp>'`:报文文件作 argv 重跑服务程序。

import (
	"fmt"
	"regexp"
	"strings"
	"tdebug/host"
	"time"
)

// WSLogItem 接口日志列表行
type WSLogItem struct {
	RowID    string `json:"rowid"`
	Service  string `json:"service"`  // wsfa001
	PID      string `json:"pid"`      // wsfa002
	Start    string `json:"start"`    // wsfa003
	End      string `json:"end"`      // wsfa004
	Duration string `json:"duration"` // wsfa005
	Code     string `json:"code"`     // wsfa006(srvcode,000=成功)
	Job      string `json:"job"`      // wsfa012(作业编号)
	ReqPath  string `json:"reqPath"`  // wsfa007
	RspPath  string `json:"rspPath"`  // wsfa008
	ReqSize  string `json:"reqSize"`  // wsfa016(字节)
	RspSize  string `json:"rspSize"`  // wsfa017
	ErrMsg   string `json:"errMsg"`   // wsfa014
}

// WSLogContent 选中日志的报文内容
type WSLogContent struct {
	Request  string `json:"request"`
	Response string `json:"response"`
}

// reRowid 行标识白名单(拼 SQL 防注入):Oracle rowid / 金仓 ctid「(页,元组)」
var reRowid = regexp.MustCompile(`^(\([0-9]+,[0-9]+\)|[A-Za-z0-9./]{1,20})$`)

// reWSLogRow 列表行解析:字段间以 | 分隔;ErrMsg(第 10 列)允许空格——
// 失败记录的错误描述如「[T100_message] 处理笔数 1, 成功 0, 失败 1」含空格,
// 用 \S+ 会整行匹配失败导致记录被静默丢弃
var reWSLogRow = regexp.MustCompile(`^(\S+)\|(\S*)\|(\S*)\|(.*)\|(.*)\|(\S*)\|(\S*)\|(\S*)\|(.*)\|(.*)\|(\S*)\|(.*)$`)

// WSLogFilter 列表过滤条件(对齐 awsq990 主查询 QBE:wsfa001 服务名 / wsfa003 开始时间范围;
// onlyFail 为本工具扩展)
type WSLogFilter struct {
	Service   string // 服务名,支持 * ? 通配
	OnlyFail  bool   // 仅失败(wsfa006<>'000')
	StartFrom string // 开始时间起(wsfa003 >=,字符串字典序即时间序)
	StartTo   string // 开始时间止(wsfa003 <=)
	Page      int    // 从 1 起
	PageSize  int    // 每页条数(50..500,默认 200)
}

// reQBEValue 时间类输入白名单(仅日期时间字符)
var reQBEValue = regexp.MustCompile(`^[0-9: -]{0,19}$`)

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
	wc := "1=1"
	if f.Service != "" {
		// 服务名(如 docno.storage / icd.erp.wo.out.query.get)支持 * ? 通配;其余字符白名单校验
		patOK := regexp.MustCompile(`^[A-Za-z0-9._*?]{1,60}$`)
		if !patOK.MatchString(f.Service) {
			wc += " AND 1=0"
		} else {
			pat := strings.ReplaceAll(strings.ReplaceAll(strings.ToUpper(f.Service), "*", "%"), "?", "_")
			wc += fmt.Sprintf(" AND UPPER(wsfa001) LIKE '%s'", strings.ReplaceAll(pat, "'", "''"))
		}
	}
	if f.OnlyFail {
		wc += " AND wsfa006 <> '000'"
	}
	for _, chk := range [][2]string{{f.StartFrom, ">="}, {f.StartTo, "<="}} {
		v := strings.TrimSpace(chk[0])
		if v == "" {
			continue
		}
		if !reQBEValue.MatchString(v) {
			return nil, false, fmt.Errorf("时间条件格式非法: %q", v)
		}
		// 纯日期(yyyy-mm-dd)作上界时补到当天末尾,否则字典序会漏掉当天记录
		if chk[1] == "<=" && len(v) == 10 {
			v += " 23:59:59"
		}
		wc += fmt.Sprintf(" AND wsfa003 %s '%s'", chk[1], v)
	}
	connStr, err := dbc.dbConnStr("ds")
	if err != nil {
		return nil, false, err
	}
	var out string
	if dbc.conn.Type == "kingbase" {
		// 金仓:ctid 作行标识;|| 遇 null 归 null,逐列 coalesce
		sql := fmt.Sprintf(`select wsfa.ctid||'|'||wsfa001||'|'||wsfa002||'|'||coalesce(substr(wsfa003,1,19),'')||'|'||coalesce(wsfa005::text,'')||'|'||coalesce(wsfa006,'')||'|'||coalesce(wsfa012,'')||'|'||coalesce(wsfa007,'')||'|'||coalesce(wsfa008,'')||'|'||coalesce(wsfa014,'')||'|'||coalesce(wsfa016::text,'')||'|'||coalesce(wsfa017::text,'') from wsfa_t wsfa where %s order by wsfa003 desc offset %d limit %d`, wc, (page-1)*size, size+1)
		out, err = dbc.exec(conn, connStr, "", sql, 40*time.Second)
	} else {
		sql := fmt.Sprintf(`set heading off
set feedback off
set trimspool on
set linesize 32767
select wsfa.rowid||'|'||wsfa001||'|'||wsfa002||'|'||substr(nvl(wsfa003,''),1,19)||'|'||nvl(wsfa005,'')||'|'||nvl(wsfa006,'')||'|'||nvl(wsfa012,'')||'|'||nvl(wsfa007,'')||'|'||nvl(wsfa008,'')||'|'||nvl(wsfa014,'')||'|'||nvl(wsfa016,'')||'|'||nvl(wsfa017,'')
from wsfa_t wsfa where %s order by wsfa003 desc offset %d rows fetch first %d rows only;`, wc, (page-1)*size, size+1)
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
		sql := fmt.Sprintf(`select wsfa.ctid||'|'||wsfa001||'|'||wsfa002||'|'||coalesce(substr(wsfa003,1,19),'')||'|'||coalesce(wsfa005::text,'')||'|'||coalesce(wsfa006,'')||'|'||coalesce(wsfa012,'')||'|'||coalesce(wsfa007,'')||'|'||coalesce(wsfa008,'')||'|'||coalesce(wsfa014,'')||'|'||coalesce(wsfa016::text,'')||'|'||coalesce(wsfa017::text,'') from wsfa_t wsfa where ctid='%s'`, rowid)
		out, err = dbc.exec(conn, connStr, "", sql, 30*time.Second)
	} else {
		sql := fmt.Sprintf(`set heading off
set feedback off
set trimspool on
set linesize 32767
set long 300000
set longchunksize 100000
select wsfa.rowid||'|'||wsfa001||'|'||wsfa002||'|'||substr(nvl(wsfa003,''),1,19)||'|'||nvl(wsfa005,'')||'|'||nvl(wsfa006,'')||'|'||nvl(wsfa012,'')||'|'||nvl(wsfa007,'')||'|'||nvl(wsfa008,'')||'|'||nvl(wsfa014,'')||'|'||nvl(wsfa016,'')||'|'||nvl(wsfa017,'')
from wsfa_t wsfa where rowid='%s';`, rowid)
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
			}
			break
		}
	}
	if item == nil {
		return nil, nil, fmt.Errorf("日志记录不存在(可能已被清理)")
	}

	content := &WSLogContent{}
	// 1) 优先读报文文件(磁盘,最完整;按日期目录轮转清理,旧文件可能不存在)
	readFile := func(path string) string {
		if path == "" {
			return ""
		}
		s, e := conn.SFTP()
		if e != nil {
			return ""
		}
		f, e := s.Open(path)
		if e != nil {
			return ""
		}
		defer f.Close()
		buf := make([]byte, 262144)
		n, _ := f.Read(buf)
		return string(buf[:n])
	}
	content.Request = readFile(item.ReqPath)
	content.Response = readFile(item.RspPath)

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
		}
		if content.Response == "" && len(rspLines) > 0 {
			content.Response = strings.Join(rspLines, "\n") + "\n(仅显示前 2000 字符,源文件已被清理)"
		}
	}

	if content.Request == "" && content.Response == "" {
		content.Request = "(报文已被清理:超过入库大小上限且源文件已不存在)"
	}
	return item, content, nil
}

// WriteReplayFiles 报文文件不存在时,把 CLOB 内容写到服务器临时文件供重放。
// 返回可用的 (reqPath, rspPath, error);路径来自日志记录或新生成的临时文件。
func WriteReplayFiles(conn *host.SSHConn, item *WSLogItem, content *WSLogContent) (reqPath, rspPath string, err error) {
	sftp, err := conn.SFTP()
	if err != nil {
		return "", "", err
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
		// CLOB 落到服务器 $TEMPDIR(aws 报文同目录规则)
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
	if reqPath, err = ensure(item.ReqPath, content.Request); err != nil {
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
