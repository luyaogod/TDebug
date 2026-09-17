package debug

// 只读 SQL:让 AI 能查一眼业务数据("这个料号在主表里到底有没有"),
// 而不必靠改入参反复重放去反推。
//
// 三层防线,缺一不可,而且**每一层都要如实说明它挡住什么、挡不住什么**:
//  1. 文本白名单(safesql.Check):挡手写的 DML/DDL、多语句、sqlplus 命令、受限包;
//  2. 库侧只读事务(SET TRANSACTION READ ONLY):挡"白名单没看出来"的常规写;
//  3. 资源与输出上限:挡"读太多"把库和上下文一起打爆。
//
// 挡住不的(文档里也要写):自治事务、函数副作用、以及"只读≠只读该企业的数据"。
//
// **账号必须由 TOPENT 解析**:企业编号与数据库账号是强关联的,上错号查不到数据 ——
// 而"查不到"会被读成"这条业务数据不存在",那是最危险的错误结论。
// 所以这里绝不硬编码账号,解析不到就直接拒绝,并把「企业→账号」回显在结果里。

import (
	"fmt"
	"strings"
	"time"

	"tdebug/host"
	"tdebug/safesql"
)

// sqlTimeoutDefault 单条只读查询的默认超时(秒)。上限见 clampSQLTimeout。
const (
	sqlTimeoutDefault = 30
	sqlTimeoutMax     = 120
	sqlKillMarginSec  = 5 // 远端 timeout 比本地 SSH 超时早这么多,好拿到明确的"被服务端终止"
)

// DBQueryResult 一次只读查询的结果。
//
// Ent/Account 是**必回显**的:让"上错号"当场可见,而不是让人从"查不到数据"倒推。
type DBQueryResult struct {
	Ent           int        `json:"ent"`
	Account       string     `json:"account"`
	Dialect       string     `json:"dialect"`
	Columns       []string   `json:"columns"`
	Rows          [][]string `json:"rows"`
	TotalRows     int        `json:"totalRows"` // 裁剪前数据库实际回来的行数(包装后最多 MaxRows+1)
	Truncated     bool       `json:"truncated"` // 有没有被裁
	TruncReason   string     `json:"truncReason,omitempty"`
	ServerLimited bool       `json:"serverLimited"` // 有没有在库侧限行(见 WrapRowLimit 的说明)
	LocalPath     string     `json:"localPath,omitempty"`
	SpillPartial  bool       `json:"spillPartial,omitempty"` // 落本地那份自己也被上限截断了
	Elapsed       float64    `json:"elapsedSeconds"`
	Notes         []string   `json:"notes,omitempty"`
}

// sqlEntCacheEntry 企业→账号映射的缓存(与 ensureRuntimeEnv 的 5 分钟缓存同风格)
type sqlEntCacheEntry struct {
	maps []EntMapping
	at   time.Time
}

const sqlEntCacheTTL = 10 * time.Minute

// entAccount 由企业编号解析出该企业该用的数据库账号(经 gzou_t)。
//
// 这一步是"上错号查不到数据"的解药:账号不再由调用方硬编码,而是从会话的 TOPENT
// 现查。解析不到就**报错**,不静默回退 —— 回退到 ds 会安安静静地查另一个 schema,
// 拿到 0 行,然后被当成"没有这条数据"。
func (m *Manager) entAccount(conn *host.SSHConn, d *dbRun, ent int) (string, error) {
	if ent <= 0 {
		return "", fmt.Errorf("企业编号无效: %d", ent)
	}
	cc := conn.Cfg()
	key := cc.Host + "|" + cc.User + "|" + d.zone
	m.entMu.Lock()
	if e, ok := m.entCache[key]; ok && time.Since(e.at) < sqlEntCacheTTL {
		maps := e.maps
		m.entMu.Unlock()
		return pickEntAccount(maps, ent)
	}
	m.entMu.Unlock()

	maps, err := dbAllMappings(conn, d)
	if err != nil {
		return "", err
	}
	m.entMu.Lock()
	m.entCache[key] = sqlEntCacheEntry{maps: maps, at: time.Now()}
	m.entMu.Unlock()
	return pickEntAccount(maps, ent)
}

// pickEntAccount 是纯函数,便于单测。
func pickEntAccount(maps []EntMapping, ent int) (string, error) {
	for _, mp := range maps {
		if mp.Ent != ent {
			continue
		}
		acct := strings.TrimSpace(mp.Account)
		// gzou003 缺失时映射表里会是 "-" 之类的占位
		if acct == "" || acct == "-" {
			return "", fmt.Errorf("企业 %d 在 gzou_t 里没有配可用账号(该企业的业务数据无法定位到 schema)", ent)
		}
		return acct, nil
	}
	known := make([]string, 0, len(maps))
	for _, mp := range maps {
		known = append(known, fmt.Sprintf("%d", mp.Ent))
	}
	return "", fmt.Errorf("企业 %d 不在 gzou_t 的启用清单里(当前有: %s)。"+
		"请确认会话的 TOPENT 是否正确:tdebug topent 可查看,`tdebug topent <企业号>` 可改",
		ent, strings.Join(known, ", "))
}

// clampSQLTimeout 把超时钳到合理区间
func clampSQLTimeout(sec int) int {
	if sec <= 0 {
		return sqlTimeoutDefault
	}
	if sec > sqlTimeoutMax {
		return sqlTimeoutMax
	}
	return sec
}

// ReadonlyQueryReq 一次只读查询的入参
type ReadonlyQueryReq struct {
	SQL     string
	Ent     int // 0 = 用会话/配置的默认企业
	Timeout int // 秒;0 = 默认
}

// RunReadonlyQuery 执行一条只读查询。
//
// 调用方须保证 SQL 已过 safesql.Check —— 这里再查一次(便宜,且不想让"谁负责校验"
// 变成一个可以踩空的口子)。
func (m *Manager) RunReadonlyQuery(conn *host.SSHConn, req ReadonlyQueryReq) (*DBQueryResult, error) {
	if err := safesql.Check(req.SQL); err != nil {
		return nil, err
	}
	if m.cfg.DB == nil {
		return nil, fmt.Errorf("当前环境未挂数据库连接(设置-环境-SSH 页选择「数据库连接」)")
	}
	if !m.cfg.DB.ReadonlySQLEnabled() {
		return nil, fmt.Errorf("该环境的只读 SQL 已在配置里关闭(debug.sshs[].db.readonlySql=false)")
	}
	d, err := resolveDBRun(conn, m.cfg)
	if err != nil {
		return nil, err
	}
	ent := req.Ent
	if ent <= 0 {
		ent = m.cfg.TopentInt()
	}
	account, err := m.entAccount(conn, d, ent)
	if err != nil {
		return nil, err
	}

	// 库侧限行:能证安全才包装,包不了就如实标注
	sql := req.SQL
	serverLimited := false
	if wrapped, ok := safesql.WrapRowLimit(d.conn.Type, req.SQL); ok {
		sql = strings.TrimSuffix(wrapped, ";")
		serverLimited = true
	}

	timeout := clampSQLTimeout(req.Timeout)
	start := time.Now()
	var out string
	if d.conn.Type == "kingbase" {
		h, p, db, err := d.kbTarget()
		if err != nil {
			return nil, err
		}
		connStr, err := d.dbConnStr(account)
		if err != nil {
			return nil, err
		}
		cmd, err := host.KbCmd(d.ksql, h, p, db, connStr)
		if err != nil {
			return nil, err
		}
		out, err = conn.OutputStdin(cmd, []byte(kbReadonlyScript(sql, timeout)), time.Duration(timeout+sqlKillMarginSec)*time.Second)
		if err != nil {
			return nil, err
		}
	} else {
		connStr, err := d.dbConnStr(account)
		if err != nil {
			return nil, err
		}
		// 远端 timeout 比本地早一点动手:这样能拿到"被服务端终止"这个明确结论,
		// 而不是本地超时后只剩一句笼统的"命令超时"
		cmd, err := host.SqlplusCmd(d.zone, d.oraT, connStr, timeout)
		if err != nil {
			return nil, err
		}
		out, err = conn.OutputStdin(cmd, []byte(oraReadonlyScript(sql)), time.Duration(timeout+sqlKillMarginSec)*time.Second)
		if err != nil {
			return nil, err
		}
	}

	res := &DBQueryResult{Ent: ent, Account: account, Dialect: d.conn.Type, ServerLimited: serverLimited, Elapsed: time.Since(start).Seconds()}
	if !serverLimited {
		res.Notes = append(res.Notes,
			fmt.Sprintf("未在数据库侧限流(该语句无法安全包装:以 WITH 开头,或自带行限制),行数上限由客户端截断"))
	}

	cols, rows, err := parseSQLOut(d.conn.Type, out)
	if err != nil {
		return nil, err
	}
	res.Columns = cols
	res.TotalRows = len(rows)
	// 包装时多取了一行,专门用来判"还有更多"
	if serverLimited && len(rows) > safesql.MaxRows {
		rows = rows[:safesql.MaxRows]
		res.Truncated = true
		res.TruncReason = "rows"
	}
	res.Rows = rows
	applyValueCap(res)
	return res, nil
}

// oraReadonlyScript Oracle 侧的 stdin 脚本。
//
// 几处不是可选的:
//   - `set define off`:不设的话 SQL 里任何 & (如 'A&B')会让 sqlplus 停下来问替换变量,挂到超时;
//   - `long 4000` / `longchunksize`:不设的话 CLOB 列会被打成噪声或按默认 long 80 静默截断,
//     让 AI 读到**错的数据**;
//   - `SET TRANSACTION READ ONLY` 必须是本事务第一条语句,所以放在所有 sqlplus 的 SET 之后。
func oraReadonlyScript(sql string) string {
	return strings.Join([]string{
		"set echo off",
		"set heading on",
		"set pagesize 50000",
		"set linesize 32767",
		"set trimspool on",
		"set feedback off",
		"set colsep '|'",
		"set define off",
		"set verify off",
		"set long 4000",
		"set longchunksize 4000",
		"set serveroutput off",
		"SET TRANSACTION READ ONLY;",
		strings.TrimSuffix(strings.TrimSpace(sql), ";") + ";",
		"exit",
		"",
	}, "\n")
}

// kbReadonlyScript 金仓侧的 stdin 脚本。
// 用**会话级**只读而不是事务级:过程内 COMMIT 之后事务级设置就失效了,会话级还在。
func kbReadonlyScript(sql string, timeoutSec int) string {
	return strings.Join([]string{
		"SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY;",
		fmt.Sprintf("SET statement_timeout = '%ds';", timeoutSec),
		strings.TrimSuffix(strings.TrimSpace(sql), ";") + ";",
		"",
	}, "\n")
}

// parseSQLOut 把 sqlplus/ksql 的输出解析成列名 + 行。
// 两边的噪声不同:sqlplus 会回显 SQL、打印 "N rows selected" 之类;ksql 的表头下有一条
// "-----+-----" 分隔线。统一下去噪。
func parseSQLOut(dialect, out string) ([]string, [][]string, error) {
	if e := sqlErrOf(out); e != "" {
		return nil, nil, fmt.Errorf("查询失败: %s", e)
	}
	var lines []string
	for _, ln := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		t := strings.TrimRight(ln, " \t")
		if strings.TrimSpace(t) == "" {
			continue
		}
		// sqlplus 的回显与统计行
		if strings.HasPrefix(t, "SQL>") || strings.HasPrefix(t, "PL/SQL") ||
			strings.Contains(t, "rows selected") || strings.Contains(t, "row selected") {
			continue
		}
		// ksql 的表头分隔线:全是 - + 空格
		if isRuleLine(t) {
			continue
		}
		lines = append(lines, t)
	}
	lines = stripLoginBanner(lines)
	if len(lines) == 0 {
		return nil, nil, nil
	}
	cols := splitRow(lines[0])
	rows := make([][]string, 0, len(lines)-1)
	for _, ln := range lines[1:] {
		rows = append(rows, splitRow(ln))
	}
	return cols, rows, nil
}

// isRuleLine 判断是不是表头下的分隔线。
//
// 两边的样子不同:sqlplus 是 `--------- | --------`(有 |),ksql 是 `--------+--------`(没有 |)。
// 一开始只认前者,后者就被当成数据行了 —— 顺带把登录横幅那条连接串顶到了表头位置。
// 判据:整行只由 - + | 和空白组成,且含一段连续 3 个以上的 - 或 +(真数据极少长这样)。
func isRuleLine(s string) bool {
	run := 0
	maxRun := 0
	for _, r := range s {
		switch r {
		case '-', '+':
			run++
			if run > maxRun {
				maxRun = run
			}
		case '|', ' ', '\t':
			run = 0
		default:
			return false
		}
	}
	return maxRun >= 3
}

// stripLoginBanner 去掉 sqlplus 登录脚本(glogin.sql)打的横幅。
//
// 实测那段是:
//
//	GLOBAL_NAME
//	------------------------------------------
//	top109ind/dsdemo@T35PRD
//
// 规则线已被 isRuleLine 滤掉,只剩标题与连接串两行。**必须去掉**:否则横幅标题会被
// 当成结果的表头,整张表的列名就全错了 —— 而列名错了比没有结果更坏,因为看起来是"对的"。
func stripLoginBanner(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	if !strings.EqualFold(strings.TrimSpace(lines[0]), "GLOBAL_NAME") {
		return lines
	}
	lines = lines[1:]
	// 紧随其后的那一行是连接标识(a/b@C);不校验得太死,免得站点改了 glogin 就漏掉
	if len(lines) > 0 && !strings.Contains(lines[0], " ") {
		lines = lines[1:]
	}
	return lines
}

func splitRow(s string) []string {
	parts := strings.Split(strings.TrimSpace(s), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// sqlErrOf 从输出里挑出数据库报错(取第一段,别把整坨噪声都塞回去)
func sqlErrOf(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "ORA-") || strings.HasPrefix(t, "SP2-") ||
			strings.HasPrefix(t, "ERROR") || strings.HasPrefix(t, "FATAL") ||
			strings.Contains(t, "cannot execute") {
			return t
		}
	}
	return ""
}

// applyValueCap 单字段值上限。**必须逐字段切,而不是逐行丢** ——
// 一行里塞一个 5MB 的 CLOB,整行丢掉会让调用方拿到"空行",那比截断更坏。
func applyValueCap(res *DBQueryResult) {
	over := false
	for i := range res.Rows {
		for j, v := range res.Rows[i] {
			if len(v) > safesql.MaxValue {
				res.Rows[i][j] = v[:safesql.MaxValue] + "…(本字段被截断)"
				over = true
			}
		}
	}
	if over {
		res.Notes = append(res.Notes, fmt.Sprintf("有字段超过 %d 字节被截断(该列可能是 CLOB)", safesql.MaxValue))
	}
}
