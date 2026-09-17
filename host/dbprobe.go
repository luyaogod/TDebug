package host

// 服务器侧数据库探测/验证(「从服务器获取」辅助,只读):
// 登录服务器自动获取数据库连接要素(oracle chenv/tnsnames;kingbase 实例发现),
// 以及用显式账号密码连库验证。运行时连接一律使用显式 host/port/service|库名。
// 账号/密码传递均走 exec 通道环境变量或 stdin,不进命令行历史。

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reEnvKV    = regexp.MustCompile(`^(ORA|SQLP|TNSADM|TWOTASK)=(\S*)\s*$`)
	reKBEnvKV  = regexp.MustCompile(`^(KSQL|KPORT|KDB)=(.*)\s*$`)
	reTNSField = regexp.MustCompile(`(HOST|PORT|SERVICE_NAME)\s*=\s*([^)\s]+)`)
)

// ZoneTNSName 区域代码 → 数据库 TNS 别名推导(31→t35dev,35→t35tst,36→t35prd,
// 39→t35pth,t→topprd)。仅「从服务器获取」辅助探测用——运行时连接一律显式 host/port/service。
func ZoneTNSName(zone string) string {
	switch zone {
	case "31":
		return "t35dev"
	case "35":
		return "t35tst"
	case "39":
		return "t35pth"
	case "t":
		return "topprd"
	default:
		return "t35prd"
	}
}

// reToolPath 服务器上工具(sqlplus/ksql)的绝对路径白名单
var reToolPath = regexp.MustCompile(`^[A-Za-z0-9_./-]{1,200}$`)

// shQuote 单引号包裹并把内部的单引号按 POSIX 惯例闭合-转义-重开。
// 凡是要作为"一个词"交给 shell 的值都走它。
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// bashLC 把一段脚本包成远端的 `bash -lc '<脚本>'`。
// 脚本内部该用引号的地方自己用(见 shQuote);这里只负责把**整段脚本**转义进外层单引号。
func bashLC(script string) string {
	return "bash -lc '" + strings.ReplaceAll(script, "'", `'\''`) + "'"
}

// ChenvCmd 拼出"加载 zone 环境"的脚本片段(带参数不弹菜单,静默)。
//
// zone 来自配置与 --zone —— 都是"人给的字符串",而这个片段会被拼进远端 shell。
// 以前它裸插且不在任何引号内:实测 `tdebug start --zone "36; id"` 能在服务器上执行任意命令。
// 现在只放行白名单字符,所以不需要(也不能)再加引号 —— 加了反而与外层 bash -lc 的单引号打架。
//
// 返回的是**脚本片段**,由调用方用 bashLC 包起来。
func ChenvCmd(zone string) (string, error) {
	if zone == "" {
		zone = "36"
	}
	if !reZone.MatchString(zone) {
		return "", fmt.Errorf("区域代码非法(只允许字母数字与 _-,长度不超过 8): %q", zone)
	}
	return fmt.Sprintf("source /u3/pub/bin/chenv %s >/dev/null 2>&1", zone), nil
}

// probeDBEnv 在服务器上探测 ORACLE_HOME / sqlplus / TWO_TASK(chenv zone 后)
func ProbeDBEnv(conn *SSHConn, zone string) (map[string]string, error) {
	cenv, err := ChenvCmd(zone)
	if err != nil {
		return nil, err
	}
	cmd := bashLC(fmt.Sprintf(`%s; echo ORA=$ORACLE_HOME; echo SQLP=$(command -v sqlplus); echo TNSADM=$TNS_ADMIN; echo TWOTASK=$TWO_TASK`, cenv))
	out, err := conn.Output(cmd, 25*time.Second)
	if err != nil && out == "" {
		return nil, fmt.Errorf("探测数据库环境失败: %w", err)
	}
	env := map[string]string{}
	for _, ln := range strings.Split(out, "\n") {
		if m := reEnvKV.FindStringSubmatch(ln); m != nil {
			env[m[1]] = m[2]
		}
	}
	if env["ORA"] == "" {
		return nil, fmt.Errorf("未探测到 ORACLE_HOME(zone %s 环境未加载?)", zone)
	}
	return env, nil
}

// probeKsqlPath 在服务器上找 ksql 客户端(command -v,退化 find 常见安装根)
func ProbeKsqlPath(conn *SSHConn) (string, error) {
	out, err := conn.Output(`
KSQL=$(command -v ksql || true)
[ -z "$KSQL" ] && KSQL=$(find /u2 /home /opt /u1 -maxdepth 8 -name ksql -type f 2>/dev/null | head -1)
echo KSQL=$KSQL
`, 40*time.Second)
	if err != nil && out == "" {
		return "", fmt.Errorf("探测金仓 ksql 失败: %w", err)
	}
	p := ""
	for _, ln := range strings.Split(out, "\n") {
		if m := reKBEnvKV.FindStringSubmatch(strings.TrimSpace(ln)); m != nil && m[1] == "KSQL" {
			p = m[2]
		}
	}
	if p == "" {
		return "", fmt.Errorf("未找到 ksql 客户端(金仓未安装或路径非标准)")
	}
	return p, nil
}

// probeKBEnv 金仓自动探测(「从服务器获取」辅助:实例/库名/端口;不改服务器状态)
func ProbeKBEnv(conn *SSHConn) (map[string]string, error) {
	out, err := conn.Output(`
KSQL=$(command -v ksql || true)
[ -z "$KSQL" ] && KSQL=$(find /u2 /home /opt /u1 -maxdepth 8 -name ksql -type f 2>/dev/null | head -1)
echo KSQL=$KSQL
DATA=$(ps -ef | grep 'kingbase' | grep -v grep | grep -o 'kingbase -D [^ ]*' | head -1 | awk '{print $3}')
echo KDB=$(basename "$(dirname "$DATA")")
PORT=$(grep -E '^[[:space:]]*port[[:space:]]*=' "$DATA/kingbase.conf" 2>/dev/null | head -1 | grep -o '[0-9]\+')
echo KPORT=${PORT:-54321}
`, 40*time.Second)
	if err != nil && out == "" {
		return nil, fmt.Errorf("探测金仓环境失败: %w", err)
	}
	env := map[string]string{}
	for _, ln := range strings.Split(out, "\n") {
		if m := reKBEnvKV.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			env[m[1]] = m[2]
		}
	}
	if env["KSQL"] == "" {
		return nil, fmt.Errorf("未找到 ksql 客户端(金仓未安装或路径非标准)")
	}
	if env["KDB"] == "" || env["KDB"] == "data" {
		return nil, fmt.Errorf("未发现运行中的金仓实例(kingbase -D 数据目录)")
	}
	return env, nil
}

// reDBAcct 数据库账号名白名单(T100 用 ds/dsdemo/dsdata 这类)
var reDBAcct = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_$#]{0,29}$`)

// KbCmd 产出在服务器上执行 ksql 的**命令行**(不含 SQL)。
//
// SQL 由调用方经 conn.OutputStdin 从 stdin 喂进去 —— 见 ssh.go 的 OutputStdin。
// 以前这里把 SQL 拼进 `-c "…"` 且只转义双引号($、反引号、\ 都没管),是一条命令注入面。
func KbCmd(ksqlPath, host, port, db, connStr string) (string, error) {
	if !reToolPath.MatchString(ksqlPath) {
		return "", fmt.Errorf("ksql 路径非法: %q", ksqlPath)
	}
	user, pass := connStr, connStr
	if i := strings.Index(connStr, "/"); i >= 0 {
		user, pass = connStr[:i], connStr[i+1:]
	}
	if !reDBAcct.MatchString(user) {
		return "", fmt.Errorf("数据库账号非法: %q", user)
	}
	if strings.ContainsAny(pass, "\n\r\x00") {
		return "", fmt.Errorf("数据库口令含非法字符(换行/空字符)")
	}
	// 口令只能作为环境变量前缀传;它仍会出现在远端的 argv 里(ps 可见)——
	// 这是既有行为,文件头注释以前写成"走 stdin 不进命令行",与实现不符,已改正。
	return fmt.Sprintf(`KINGBASE_PASSWORD=%s %s -w -h %s -p %s -U %s -d %s -t -A -F '|'`,
		shQuote(pass), ksqlPath, shQuote(host), shQuote(port), shQuote(user), shQuote(db)), nil
}

// SqlplusCmd 产出在服务器上执行 sqlplus 的**命令行**(不含 SQL)。
//
// SQL 由调用方经 conn.OutputStdin 从 stdin 喂进去。以前这里是
// `echo "<SQL>" | sqlplus` 再整体套 `bash -lc '...'`:两层解析,第二层里 $()/反引号
// 照常展开,一个引号就能把后面的 ; | 变成命令分隔符 —— 是完整的命令注入,不是理论风险。
//
// killAfterSec > 0 时套一层远端 `timeout -s TERM N`:Oracle 侧没有任何服务端超时机制,
// 本地 SSH 层的超时又只是"不再等"、不杀进程(靠 sshd 回收,不确定),所以这一步是必须的。
func SqlplusCmd(zone, sqlplusPath, connStr string, killAfterSec int) (string, error) {
	env, err := ChenvCmd(zone)
	if err != nil {
		return "", err
	}
	p := sqlplusPath
	if p == "" {
		p = "sqlplus"
	}
	if !reToolPath.MatchString(p) {
		return "", fmt.Errorf("sqlplus 路径非法: %q", p)
	}
	if strings.ContainsAny(connStr, "\n\r\x00") {
		return "", fmt.Errorf("连接串含非法字符(换行/空字符)")
	}
	tool := p
	if killAfterSec > 0 {
		tool = fmt.Sprintf("timeout -s TERM %d %s", killAfterSec, p)
	}
	return bashLC(fmt.Sprintf("%s; exec %s -S %s", env, tool, shQuote(connStr))), nil
}

// parseTNS 解析服务器 tnsnames.ora 里 TNS 别名的地址/服务(「从服务器获取」辅助用)
func ParseTNS(conn *SSHConn, oracleHome, tns string) (host, port, service string, err error) {
	if oracleHome == "" {
		return "", "", "", fmt.Errorf("缺少 ORACLE_HOME,无法定位 tnsnames.ora")
	}
	path := oracleHome + "/network/admin/tnsnames.ora"
	upper := strings.ToUpper(tns)
	out, _ := conn.Output(fmt.Sprintf(
		`awk -v A=%q 'toupper($0) ~ "^" A "[ \t]*=" {f=1; next}
f && ($0 ~ /^[ \t]*$/ || $0 ~ /^[A-Za-z0-9_]+[ \t]*=/) {exit}
f {print}' %s 2>/dev/null | grep -E 'HOST|PORT|SERVICE_NAME' | head -6`, upper, path), 15*time.Second)
	for _, ln := range strings.Split(out, "\n") {
		for _, m := range reTNSField.FindAllStringSubmatch(ln, -1) {
			switch m[1] {
			case "HOST":
				host = m[2]
			case "PORT":
				port = m[2]
			case "SERVICE_NAME":
				service = m[2]
			}
		}
	}
	if host == "" {
		err = fmt.Errorf("tnsnames.ora 未解析到 %s", tns)
	}
	return
}

// DBProbeReq 设置页「从服务器获取」请求:SSH 连接 + 数据库类型
type DBProbeReq struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Zone     string `json:"zone"`
	Type     string `json:"type"` // oracle | kingbase
}

// DBProbeOut 探测结果:拿到的字段回填 DB 表单,拿不到的留空由用户手填
type DBProbeOut struct {
	Type       string `json:"type"`
	TNS        string `json:"tns,omitempty"`      // oracle: TNS 别名(按区域推导/服务器 TWO_TASK)
	Port       int    `json:"port,omitempty"`     // oracle: tnsnames 端口 / kingbase: 实例端口
	Database   string `json:"database,omitempty"` // kingbase: 库名
	Sqlplus    string `json:"sqlplus,omitempty"`  // oracle: 服务器 sqlplus 路径
	OracleHome string `json:"oracleHome,omitempty"`
	TwoTask    string `json:"twoTask,omitempty"`
	Host       string `json:"host,omitempty"` // oracle tnsnames 解析出的实际库地址
	Service    string `json:"service,omitempty"`
	Note       string `json:"note,omitempty"` // 未获取到时的说明
}

// ProbeDBConfig 登录服务器自动获取数据库连接要素(「从服务器获取」辅助,只读)。
// oracle → chenv 环境 + tnsnames 解析;kingbase → 实例发现(ksql/端口/库名)。
// 解析结果仅作回填参考;运行时连接一律使用显式 host/port/service。
func ProbeDBConfig(req DBProbeReq) (*DBProbeOut, error) {
	ssh := SSHConfig{Host: req.Host, User: req.User, Password: req.Password}
	if req.Port > 0 {
		ssh.Port = req.Port
	} else {
		ssh.Port = 22
	}
	conn, err := Dial(ssh)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer conn.Close()

	out := &DBProbeOut{Type: req.Type}
	if req.Type == "kingbase" {
		env, err := ProbeKBEnv(conn)
		if err != nil {
			out.Note = err.Error()
			return out, nil // 拿不到也让用户手填,不报错
		}
		out.Port, _ = strconv.Atoi(env["KPORT"])
		out.Database = env["KDB"]
		return out, nil
	}
	// oracle
	zone := req.Zone
	if zone == "" {
		zone = "36"
	}
	tns := ZoneTNSName(zone)
	env, err := ProbeDBEnv(conn, zone)
	if err != nil {
		out.TNS = tns // chenv 拿不到也给出按区域推导的 TNS
		out.Note = err.Error()
		return out, nil
	}
	out.TNS = env["TWOTASK"]
	if out.TNS == "" {
		out.TNS = tns
	}
	out.Sqlplus = env["SQLP"]
	out.OracleHome = env["ORA"]
	out.TwoTask = env["TWOTASK"]
	if h, p, s, e := ParseTNS(conn, env["ORA"], out.TNS); e == nil {
		out.Host, out.Service = h, s
		out.Port, _ = strconv.Atoi(p)
	}
	return out, nil
}

// firstLines 取输出前 n 行(清理空行)
func FirstLines(s string, n int) string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out = append(out, ln)
		}
		if len(out) >= n {
			break
		}
	}
	return strings.Join(out, "; ")
}

// DBAccVerifyReq 账号验证请求(设置页账号清单「验证」;只读)。
// 内嵌 DBProbeReq 提供 SSH 登录要素(password=SSH 密码);目标库要素显式给出:
// oracle 用 dbHost/dbPort/dbSvc;kingbase 用 dbHost/dbPort/dbDatabase。
type DBAccVerifyReq struct {
	DBProbeReq
	Account    string `json:"account"`              // 待验证账号(schema 名)
	AcctPass   string `json:"acctPassword"`         // 待验证账号密码
	DBHost     string `json:"dbHost,omitempty"`     // 库主机(服务器侧可达;空=127.0.0.1)
	DBPort     int    `json:"dbPort,omitempty"`     // 库端口(0=默认 1521/54321)
	DBSvc      string `json:"dbSvc,omitempty"`      // oracle: 服务名
	DBDatabase string `json:"dbDatabase,omitempty"` // kingbase: 库名
}

// VerifyDBAcct 服务器侧以显式 账号/密码 连库执行 select 1(账号清单行内验证用)。
// oracle: chenv zone + sqlplus 账号/密码@//host:port/service;kingbase: ksql -h host。
func VerifyDBAcct(req DBAccVerifyReq) error {
	ssh := SSHConfig{Host: req.Host, User: req.User, Password: req.Password}
	if req.Port > 0 {
		ssh.Port = req.Port
	} else {
		ssh.Port = 22
	}
	conn, err := Dial(ssh)
	if err != nil {
		return fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer conn.Close()

	creds := req.Account + "/" + req.AcctPass
	var out string
	if req.Type == "kingbase" {
		ksql, err := ProbeKsqlPath(conn)
		if err != nil {
			return err
		}
		host, port, db := req.DBHost, strconv.Itoa(req.DBPort), req.DBDatabase
		if host == "" {
			host = "127.0.0.1"
		}
		if req.DBPort == 0 {
			port = "54321"
		}
		if db == "" {
			return fmt.Errorf("缺少库名(dbDatabase)")
		}
		kbCmdLine, kerr := KbCmd(ksql, host, port, db, creds)
		if kerr != nil {
			return kerr
		}
		out, err = conn.OutputStdin(kbCmdLine, []byte("select 'OK';\n"), 30*time.Second)
	} else {
		zone := req.Zone
		if zone == "" {
			zone = "36"
		}
		if req.DBSvc == "" {
			return fmt.Errorf("缺少服务名(dbSvc)")
		}
		host := req.DBHost
		if host == "" {
			host = "127.0.0.1"
		}
		port := req.DBPort
		if port == 0 {
			port = 1521
		}
		addr := net.JoinHostPort(host, strconv.Itoa(port)) + "/" + req.DBSvc
		spCmdLine, serr := SqlplusCmd(zone, "", creds+"@//"+addr, 0)
		if serr != nil {
			return serr
		}
		// SQL 走 stdin(以前是 echo "<SQL>" 拼进命令行,见 SqlplusCmd 的注释)
		out, err = conn.OutputStdin(spCmdLine,
			[]byte("set heading off\nset feedback off\nselect 'OK' from dual;\nexit\n"), 30*time.Second)
	}
	if err != nil {
		return fmt.Errorf("连接失败: %s", FirstLines(out, 4))
	}
	if strings.Contains(out, "ORA-") || strings.Contains(out, "ERROR") || strings.Contains(out, "error") {
		return fmt.Errorf("连接失败: %s", FirstLines(out, 4))
	}
	return nil
}
