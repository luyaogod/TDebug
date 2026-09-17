package debug

// 服务器侧数据库执行:登录服务器 → 用显式连接(dbconfig.Connection:
// host/port/service|database + 账号清单)连库 —— Oracle 走 EZCONNECT
// (sqlplus 账号/密码@//host:port/service),金仓走 ksql -h host。
// gzou_t/gzzz_t 等 T100 字典查询仍按 TOPENT 语义:企业→账号由 gzou_t 现查,
// 密码查连接的账号清单(含主账号),未收录回退 T100 惯例 账号=密码。
// tnsnames/chenv/ORA 环境读取仅保留给「从服务器获取」辅助(ProbeDBConfig)。

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tdebug/dbconfig"
	"tdebug/host"
)

// EntMapping 企业 → 账号映射
type EntMapping struct {
	Ent     int    `json:"ent"`
	Account string `json:"account"` // gzou003:账号/schema 名
}

// DBProbeResult 单账号连接探查结果
type DBProbeResult struct {
	Ent     int    `json:"ent"`
	Account string `json:"account"`
	Host    string `json:"host"`
	Port    string `json:"port"`
	Service string `json:"service"`
	Connect bool   `json:"connect"` // 用账号+密码(清单/惯例)能否连通
	Error   string `json:"error,omitempty"`
}

// DBReport 完整探查报告
type DBReport struct {
	Zone     string            `json:"zone"`
	TNS      string            `json:"tns"` // 显示用:oracle service / kingbase 库名
	Mappings []EntMapping      `json:"mappings"`
	Probe    *DBProbeResult    `json:"probe,omitempty"`
	Env      map[string]string `json:"env,omitempty"` // 探测到的环境(sqlplus/oracleHome 等)
}

var (
	reGzouMap  = regexp.MustCompile(`^\s*(\d+)\|(\S+)\s*$`)
	reEnvKV    = regexp.MustCompile(`^(ORA|SQLP|TNSADM|TWOTASK)=(\S*)\s*$`)
	reKBEnvKV  = regexp.MustCompile(`^(KSQL|KPORT|KDB)=(.*)\s*$`)
	reTNSField = regexp.MustCompile(`(HOST|PORT|SERVICE_NAME)\s*=\s*([^)\s]+)`)
)

// ---- 运行时执行上下文(显式连接) ----

// dbRun 服务器侧数据库执行上下文:绑定一条显式连接 + 服务器工具路径。
type dbRun struct {
	conn *dbconfig.Connection // 显式连接(host/port/service|database/accounts/sqlplus…);nil=未挂库
	zone string               // 服务器登录区域(chenv 环境前缀)
	ksql string               // kingbase: ksql 绝对路径(探测缓存)
	oraT string               // oracle: sqlplus 路径(conn.Sqlplus 或探测;空=PATH 找)
}

// connAddrOracle 返回 oracle 显式地址 host:port/service(EZCONNECT 用)
func connAddrOracle(c *dbconfig.Connection) (string, error) {
	if c == nil || c.Host == "" {
		return "", fmt.Errorf("数据库连接缺少主机地址(host)")
	}
	if c.Svc() == "" {
		return "", fmt.Errorf("Oracle 连接缺少服务名(service)")
	}
	port := c.Port
	if port == 0 {
		port = 1521
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(port)) + "/" + c.Svc(), nil
}

// acctConnStr 组装 "账号/密码":密码优先连接账号清单(含主账号),未收录回退 账号=密码 惯例
func acctConnStr(c *dbconfig.Connection, account string) string {
	pass := account
	if p, ok := c.PasswordFor(account); ok {
		pass = p
	}
	return account + "/" + pass
}

// resolveDBRun 解析当前生效连接并探测服务器工具路径;未挂库返回错误
// 客户端路径探测结果缓存:ProbeDBEnv/ProbeKsqlPath 都要在服务器上 source T100 环境
// (一次 SSH exec,秒级),而结果只与 主机+账号+区域+库类型 有关。
// 接口日志这类"每次点击都查一次库"的路径上,省掉这次探测是最大的一笔开销。
// 与 Manager.envCache 同风格:互斥 + TTL;只缓存探测成功的路径(失败不缓存,下次重试)。
type dbProbeEntry struct {
	oraT string // Oracle:sqlplus 绝对路径
	ksql string // 金仓:ksql 绝对路径
	at   time.Time
}

var (
	dbProbeMu    sync.Mutex
	dbProbeCache = map[string]dbProbeEntry{}
)

const dbProbeTTL = 10 * time.Minute

func resolveDBRun(sshConn *host.SSHConn, cfg *Config) (*dbRun, error) {
	if cfg.DB == nil {
		return nil, fmt.Errorf("当前环境未挂数据库连接(设置-环境-SSH 页选择「数据库连接」)")
	}
	d := &dbRun{conn: cfg.DB, zone: cfg.Zone}
	cc := sshConn.Cfg()
	key := cc.Host + "|" + cc.User + "|" + cfg.Zone + "|" + cfg.DB.Type
	dbProbeMu.Lock()
	if e, ok := dbProbeCache[key]; ok && time.Since(e.at) < dbProbeTTL {
		d.ksql, d.oraT = e.ksql, e.oraT
		dbProbeMu.Unlock()
		return d, nil
	}
	dbProbeMu.Unlock()

	var err error
	if cfg.DB.Type == "kingbase" {
		d.ksql, err = host.ProbeKsqlPath(sshConn)
		if err != nil {
			return nil, err
		}
	} else {
		// oracle:sqlplus 路径一律自动探测(chenv zone 后 command -v;失败留空由 PATH 兜底)
		zone := cfg.Zone
		if zone == "" {
			zone = "36"
		}
		if env, e := host.ProbeDBEnv(sshConn, zone); e == nil {
			d.oraT = env["SQLP"]
		} else {
			// 探测失败:本次按 PATH 兜底,且不写缓存(下次重试,避免把偶发失败固化 10 分钟)
			return d, nil
		}
	}
	dbProbeMu.Lock()
	dbProbeCache[key] = dbProbeEntry{oraT: d.oraT, ksql: d.ksql, at: time.Now()}
	dbProbeMu.Unlock()
	return d, nil
}

// dbConnStr 取该库下账号的完整连接串:kingbase=账号/密码;oracle=账号/密码@//host:port/service
func (d *dbRun) dbConnStr(account string) (string, error) {
	creds := acctConnStr(d.conn, account)
	if d.conn.Type != "kingbase" {
		addr, err := connAddrOracle(d.conn)
		if err != nil {
			return "", err
		}
		return creds + "@//" + addr, nil
	}
	return creds, nil
}

// kbTarget 金仓显式目标(host/port/db),host 空回退服务器本机
func (d *dbRun) kbTarget() (host, port, db string, err error) {
	if d.conn == nil || d.conn.Database == "" {
		return "", "", "", fmt.Errorf("金仓连接缺少库名(database)")
	}
	host, port, db = d.conn.Host, strconv.Itoa(d.conn.Port), d.conn.Database
	if host == "" {
		host = "127.0.0.1"
	}
	if d.conn.Port == 0 {
		port = "54321"
	}
	return
}

// exec 执行查询:金仓走 ksql(kbSQL),Oracle 走 sqlplus(oracleSQL)。
// connStr 由 dbConnStr(账号)生成,须与方言匹配;ds 系统账号为 gzou_t/gzzz_t 解析入口。
//
// SQL 一律经 **stdin** 送(见 host.OutputStdin):不拼进命令行,服务器的 shell 就不会
// 再解析一遍 SQL 文本。命令串里只剩工具路径、账号与连接参数,都过了白名单。
func (d *dbRun) exec(conn *host.SSHConn, connStr, oracleSQL, kbSQL string, timeout time.Duration) (string, error) {
	if d.conn.Type == "kingbase" {
		h, p, db, err := d.kbTarget()
		if err != nil {
			return "", err
		}
		cmd, err := host.KbCmd(d.ksql, h, p, db, connStr)
		if err != nil {
			return "", err
		}
		return conn.OutputStdin(cmd, []byte(kbSQL), timeout)
	}
	cmd, err := host.SqlplusCmd(d.zone, d.oraT, connStr, 0)
	if err != nil {
		return "", err
	}
	return conn.OutputStdin(cmd, []byte(oracleSQL), timeout)
}

// dbAllMappings 查询 gzou_t 全部企业→账号映射(用 ds 系统账号)
func dbAllMappings(conn *host.SSHConn, d *dbRun) ([]EntMapping, error) {
	connStr, err := d.dbConnStr("ds")
	if err != nil {
		return nil, err
	}
	kbSQL := `select gzou001,coalesce(gzou003,'-') from gzou_t where gzoustus='Y' order by gzou001`
	oraSQL := "set heading off\nset feedback off\nselect gzou001||'|'||nvl(gzou003,'-') from gzou_t where gzoustus='Y' order by gzou001;"
	out, err := d.exec(conn, connStr, oraSQL, kbSQL, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("查询 gzou_t 失败: %w (%s)", err, host.FirstLines(out, 3))
	}
	var maps []EntMapping
	for _, ln := range strings.Split(out, "\n") {
		if m := reGzouMap.FindStringSubmatch(ln); m != nil {
			ent, _ := strconv.Atoi(m[1])
			maps = append(maps, EntMapping{Ent: ent, Account: m[2]})
		}
	}
	if len(maps) == 0 {
		return nil, fmt.Errorf("gzou_t 无有效企业记录(输出: %s)", host.FirstLines(out, 5))
	}
	sort.Slice(maps, func(i, j int) bool { return maps[i].Ent < maps[j].Ent })
	return maps, nil
}

// JobResolve 作业解析结果(gendbg 原版语义)
type JobResolve struct {
	Prog      string // 实体程序编号(gzza001/gzzz002)
	Module    string // 模块代码(gzzz005)
	LaunchRef string // gzza004 去 "$FGLRUN" 前缀的启动引用(如 $CINi/ainq120_wf),由选区 shell 展开权威路径
	Extra     string // gzzz004 额外参数(gendbg 拼在程序名后)
}

// dbResolveJob 按作业编号查实体程序与启动引用(对齐 gendbg.4gl:
// SELECT gzza001,gzzz004,gzza004,gzzz005 FROM gzzz_t INNER JOIN gzza_t ON gzza001=gzzz002)。
// 一个程序(gzzz002)可被多个作业编号共用。无记录返回零值。
func dbResolveJob(conn *host.SSHConn, d *dbRun, job string) (JobResolve, error) {
	connStr, err := d.dbConnStr("ds")
	if err != nil {
		return JobResolve{}, err
	}
	kbSQL := fmt.Sprintf(`select gzza001,coalesce(gzzz004,' '),coalesce(gzza004,' '),coalesce(gzzz005,'-') from gzzz_t inner join gzza_t on gzza001=gzzz002 where gzzz001='%s'`, job)
	oraSQL := "set heading off\nset feedback off\nselect gzza001||'|'||nvl(gzzz004,' ')||'|'||nvl(gzza004,' ')||'|'||nvl(gzzz005,'-') from gzzz_t inner join gzza_t on gzza001=gzzz002 where gzzz001='" + job + "';"
	out, err := d.exec(conn, connStr, oraSQL, kbSQL, 25*time.Second)
	if err != nil {
		return JobResolve{}, fmt.Errorf("查询 gzzz_t 失败: %w (%s)", err, host.FirstLines(out, 3))
	}
	var jr JobResolve
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "ORA-") || strings.Contains(ln, "ERROR") {
			return JobResolve{}, fmt.Errorf("gzzz_t 查询出错: %s", host.FirstLines(out, 3))
		}
		ln = strings.TrimSpace(ln)
		parts := strings.Split(ln, "|")
		if len(parts) < 4 || parts[0] == "" {
			continue
		}
		// gendbg 拼装时 gzza004 = "$FGLRUN $<模块变量>/<程序>";去掉前缀留启动引用($变量 交 shell 展开)
		jr = JobResolve{Prog: parts[0], Extra: strings.TrimSpace(parts[1]), Module: parts[3]}
		jr.LaunchRef = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(parts[2]), "$FGLRUN"))
		break
	}
	return jr, nil
}

// dbConnectTest 用账号连接验证:密码优先连接账号清单,未收录回退 账号=密码
func dbConnectTest(conn *host.SSHConn, d *dbRun, account string) error {
	connStr, err := d.dbConnStr(account)
	if err != nil {
		return err
	}
	out, err := d.exec(conn, connStr,
		"set heading off\nset feedback off\nselect 'OK' from dual;", `select 'OK'`, 30*time.Second)
	if err != nil {
		msg := host.FirstLines(out, 4)
		return fmt.Errorf("连接失败: %s", msg)
	}
	if strings.Contains(out, "ORA-") || strings.Contains(out, "ERROR") || strings.Contains(out, "error") {
		return fmt.Errorf("连接失败: %s", host.FirstLines(out, 4))
	}
	return nil
}

// ProbeDB 登录服务器按当前环境探查:ent>0 验证该企业账号连通性;ent<=0 仅列映射。
// 连接完全使用显式 dbconfig.Connection(服务器侧可达的 host/port/service|库名)。
func ProbeDB(cfg *Config, ent int) (*DBReport, error) {
	conn, err := host.Dial(cfg.SSH)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer conn.Close()

	zone := cfg.Zone
	if zone == "" {
		zone = "36"
	}
	if cfg.DB == nil {
		return nil, fmt.Errorf("当前环境未挂数据库连接(设置-环境-SSH 页选择「数据库连接」)")
	}
	d, err := resolveDBRun(conn, cfg)
	if err != nil {
		return nil, err
	}
	report := &DBReport{Zone: zone, TNS: cfg.DB.Svc()}
	report.Env = map[string]string{}
	if cfg.DB.Type == "kingbase" {
		h, p, db, _ := d.kbTarget()
		report.TNS = db
		report.Env = map[string]string{"ksql": d.ksql, "host": h, "port": p, "database": db}
	} else {
		report.Env = map[string]string{
			"host":    cfg.DB.Host,
			"port":    strconv.Itoa(cfg.DB.Port),
			"service": cfg.DB.Svc(),
			"sqlplus": d.oraT,
		}
	}
	maps, err := dbAllMappings(conn, d)
	if err != nil {
		return nil, err
	}
	report.Mappings = maps
	if ent <= 0 {
		return report, nil // 仅列映射
	}
	account := ""
	for _, m := range maps {
		if m.Ent == ent {
			account = m.Account
			break
		}
	}
	if account == "" {
		return nil, fmt.Errorf("企业 %d 不存在于 gzou_t(可用 tdebug db 查看全部)", ent)
	}
	res := &DBProbeResult{Ent: ent, Account: account}
	if cfg.DB.Type == "kingbase" {
		h, p, db, _ := d.kbTarget()
		res.Host, res.Port, res.Service = h, p, db
	} else {
		res.Host, res.Port, res.Service = cfg.DB.Host, strconv.Itoa(cfg.DB.Port), cfg.DB.Svc()
	}
	if err := dbConnectTest(conn, d, account); err != nil {
		res.Error = err.Error()
	} else {
		res.Connect = true
	}
	report.Probe = res
	return report, nil
}
