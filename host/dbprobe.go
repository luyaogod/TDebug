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

// chenvCmd 拼出"加载 zone 环境"的 bash 前缀(带参数不弹菜单,静默)
func ChenvCmd(zone string) string {
	return fmt.Sprintf("source /u3/pub/bin/chenv %s >/dev/null 2>&1", zone)
}

// probeDBEnv 在服务器上探测 ORACLE_HOME / sqlplus / TWO_TASK(chenv zone 后)
func ProbeDBEnv(conn *SSHConn, zone string) (map[string]string, error) {
	cmd := fmt.Sprintf(`bash -lc '%s; echo ORA=$ORACLE_HOME; echo SQLP=$(command -v sqlplus); echo TNSADM=$TNS_ADMIN; echo TWOTASK=$TWO_TASK'`, ChenvCmd(zone))
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

// kbCmd 拼出在服务器上执行 ksql 的命令(显式 host/port/db;KINGBASE_PASSWORD 传密)
func KbCmd(ksqlPath, host, port, db, connStr, sql string) string {
	q := strings.ReplaceAll(sql, `"`, `\"`)
	user, pass := connStr, connStr
	if i := strings.Index(connStr, "/"); i >= 0 {
		user, pass = connStr[:i], connStr[i+1:]
	}
	return fmt.Sprintf(`KINGBASE_PASSWORD=%s %s -w -h %s -p %s -U %s -d %s -t -A -F '|' -c "%s"`,
		pass, ksqlPath, host, port, user, db, q)
}

// sqlplusRun 拼出在服务器上执行 sqlplus 的命令:chenv zone 提供运行环境,
// 连接串为显式 EZCONNECT "账号/密码@//host:port/service";sqlplusPath 空则用 PATH 内 sqlplus
func SqlplusRun(zone, sqlplusPath, connStr, sql string) string {
	q := strings.ReplaceAll(sql, "'", "'\\''")
	p := sqlplusPath
	if p == "" {
		p = "sqlplus"
	}
	return fmt.Sprintf(`bash -lc '%s; echo "%s" | %s -S %s'`, ChenvCmd(zone), q, p, connStr)
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
		out, err = conn.Output(KbCmd(ksql, host, port, db, creds, `select 'OK'`), 30*time.Second)
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
		sql := "set heading off\nset feedback off\nselect 'OK' from dual;"
		addr := net.JoinHostPort(host, strconv.Itoa(port)) + "/" + req.DBSvc
		out, err = conn.Output(SqlplusRun(zone, "", creds+"@//"+addr, sql), 30*time.Second)
	}
	if err != nil {
		return fmt.Errorf("连接失败: %s", FirstLines(out, 4))
	}
	if strings.Contains(out, "ORA-") || strings.Contains(out, "ERROR") || strings.Contains(out, "error") {
		return fmt.Errorf("连接失败: %s", FirstLines(out, 4))
	}
	return nil
}
