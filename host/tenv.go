package host

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// RuntimeEnv 登录后从服务器环境脚本动态获取的 T100 路径(与标准 debug 同源)。
// T100 无 $TOPDIR 变量:登录区域经站点 profile 的 case 表得到 ZONE 目录名,
// topenv 用 TOP=/u1/$ZONE 派生全部路径,ERP/COM 由 TOP 派生;模块变量由 topsys 扫描生成。
// 动态路径是唯一权威来源:无静态 topDir/moduleRoots 配置,获取失败由调用方报错。
type RuntimeEnv struct {
	TOP             string    // /u1/t35tst
	ERP             string    // $TOP/erp
	COM             string    // $TOP/com
	FGLDIR          string    // /u1/genero/fgl
	FGLResourcePath string    // $ERP:$COM
	Topent          string    // 登录回读的 $TOPENT(选区/环境脚本给的当前企业;未设置时为空)
	FetchedAt       time.Time // 获取时间(缓存 TTL 判定)
}

// Valid 动态环境是否可用
func (e *RuntimeEnv) Valid() bool { return e != nil && e.TOP != "" && e.ERP != "" }

// 探针回显协议:两个定界符之间只回显真实的变量名与值(TOP=/u1/t35prd 这种)。
// TDBG 是本工具自己的本地标记命名空间(见 README 的 TDBG_RAW),不是远端环境变量名 ——
// 之所以需要定界符,是因为 PTY 只有纯文本:登录脚本自己也会打印 ZONE = t35prd、
// TOPENT   = 99 这类行,没有区间就无法区分「我方请求的值」和「服务器自己打印的内容」。
const (
	TEnvBegin = "TDBG-BEGIN"
	TEnvEnd   = "TDBG-END"
)

// TEnvProbe 回读 T100 环境变量的探针(调用方自行补 \r 或塞进脚本)。
// 登录式探针与会话登录共用本函数,保证两处格式永远一致。
// ${TOPENT-} 用 POSIX 默认值展开:TOPENT 可能未设置,不能因 profile 开了 set -u
// 让整条回显报错;未设置时回显为空本身就是有效信息。
func TEnvProbe() string {
	return "echo " + TEnvBegin +
		"; echo TOP=$TOP; echo ERP=$ERP; echo COM=$COM; echo FGLDIR=$FGLDIR" +
		"; echo FGLRESOURCEPATH=$FGLRESOURCEPATH; echo TOPENT=${TOPENT-}" +
		"; echo " + TEnvEnd
}

// reTEnvKV 匹配定界符内的取值行。TOPENT 排在 TOP 之前:后者是前者的前缀,
// 顺序写反时靠回溯仍能匹配上,显式前置可避免后续维护踩坑。
var reTEnvKV = regexp.MustCompile(`^(TOPENT|TOP|FGLRESOURCEPATH|FGLDIR|ERP|COM)=(\S*)\s*$`)

// TEnvParser 解析探针回显:只有严格落在 TEnvBegin/TEnvEnd 两个独立行之间的取值行
// 才会被采纳。这样挡掉两类污染 ——
//   - PTY 会把我方发去的整条命令原样回显,该行虽含定界符文本,但不是独立一行;
//   - 命令超宽被终端换行 / 行编辑重绘产生的碎片,同样落在区间之外。
//
// Feed 传参需为已 TrimSpace 的整行。
type TEnvParser struct {
	env     RuntimeEnv
	inBlock bool
}

// Feed 处理一行,返回 true 表示已见到结束定界符(调用方可停止读取)。
func (p *TEnvParser) Feed(line string) bool {
	switch line {
	case TEnvBegin:
		p.inBlock = true
		return false
	case TEnvEnd:
		return true
	}
	if !p.inBlock {
		return false
	}
	m := reTEnvKV.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	switch m[1] {
	case "TOP":
		p.env.TOP = m[2]
	case "ERP":
		p.env.ERP = m[2]
	case "COM":
		p.env.COM = m[2]
	case "FGLDIR":
		p.env.FGLDIR = m[2]
	case "FGLRESOURCEPATH":
		p.env.FGLResourcePath = m[2]
	case "TOPENT":
		p.env.Topent = m[2]
	}
	return false
}

// Env 返回已解析到的环境(FetchedAt 由调用方按需补)。
func (p *TEnvParser) Env() *RuntimeEnv { return &p.env }

// reZone 区域代码白名单(31/35/36/39/t/36k/1 等):允许数字/字母/下划线/连字符,防注入
var reZone = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)

// ProbeTEnv 探测登录区域对应的 T100 环境变量。
// 配置 zone 是「登录菜单选项号」(如恒烁 1/2/3/4、109 31/35/36/39),不是 ZONE 变量字符串;
// 因此优先「真实登录」式探测:开 PTY 等登录菜单,敲入选项号,由登录脚本完成
// 选项号→ZONE 字符串 的映射后回读 TOP/ERP/COM——与会话登录完全同源,各站点菜单自动适配。
// 登录式失败(无菜单/菜单拒绝)回退旧版 chenv 链脚本(选项号直接当 ZONE,仅适用于
// 菜单码与 ZONE 值重合的站点)。
func ProbeTEnv(conn *SSHConn, zone string) (*RuntimeEnv, error) {
	if !reZone.MatchString(zone) {
		return nil, fmt.Errorf("区域代码非法: %q", zone)
	}
	if env, err := probeTEnvLogin(conn, zone); err == nil {
		return env, nil
	} else if conn != nil {
		log.Printf("[tenv] 登录式探针失败,回退脚本探针: %v", err)
	}
	return probeTEnvScript(conn, zone)
}

// probeTEnvLogin 「真实登录」式环境探针:PTY 登录 → 等区域菜单 → 敲入选项号 →
// 等 shell 提示符 → 回显 T100 环境变量并解析。与 Session.Launch 的登录流程同源。
func probeTEnvLogin(conn *SSHConn, zone string) (*RuntimeEnv, error) {
	pty, err := conn.NewPTY(200, 50)
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	defer close(done)
	defer pty.Close()
	// 输出泵:LineParser 按行/半行出行,提示符后面没有换行,
	// 半行已是提示符形态就立即出行(PS1 与 (fgldb) 都是"无换行"输出,否则永远等不到)
	lineCh := make(chan string, 256)
	go func() {
		defer close(lineCh)
		pr := &LineParser{}
		buf := make([]byte, 8192)
		for {
			select {
			case <-done:
				return
			default:
			}
			n, err := pty.Read(buf)
			if n > 0 {
				for _, ln := range pr.Feed(buf[:n]) {
					select {
					case lineCh <- ln:
					case <-done:
						return
					}
				}
				if partial := pr.PartialStr(); partial != "" {
					if IsBarePrompt(partial) || ReShellPrompt.MatchString(partial) {
						select {
						case lineCh <- pr.FlushPartial():
						case <-done:
							return
						}
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	waitFor := func(re *regexp.Regexp, timeout time.Duration, what string) error {
		deadline := time.After(timeout)
		for {
			select {
			case ln, ok := <-lineCh:
				if !ok {
					return fmt.Errorf("等待 %s 时连接已关闭(登录脚本退出?)", what)
				}
				if re.MatchString(ln) {
					return nil
				}
			case <-deadline:
				return fmt.Errorf("等待 %s 超时", what)
			}
		}
	}
	// 1. 登录区域菜单
	if err := waitFor(ReLoginMenu, 12*time.Second, "区域菜单"); err != nil {
		return nil, err
	}
	// 2. 敲入区域选项号(菜单码 → ZONE 字符串由登录脚本映射,与手工登录一致)
	// 菜单文本出现到 read 就绪有微小窗口,先等一拍再敲,避免输入被行编辑吞掉
	time.Sleep(500 * time.Millisecond)
	if err := pty.Write(zone + "\r"); err != nil {
		return nil, err
	}
	// 3. 等 shell 提示符(选项号非法时登录脚本 *)exit,连接关闭会快速失败)
	if err := waitFor(ReShellPrompt, 15*time.Second, "shell 提示符"); err != nil {
		return nil, err
	}
	// 4. 回显环境变量(共用 TEnvProbe:定界符 + 真实变量名)
	if err := pty.Write(TEnvProbe() + "\r"); err != nil {
		return nil, err
	}
	// 5. 解析回显(只认定界符之间的取值行)
	env := &RuntimeEnv{}
	var p TEnvParser
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ln, ok := <-lineCh:
			if !ok {
				return nil, fmt.Errorf("回显 T100 环境变量时连接已关闭")
			}
			if p.Feed(strings.TrimSpace(ln)) {
				env = p.Env()
				if !env.Valid() {
					return nil, fmt.Errorf("回显未包含 TOP/ERP(区域 %s 环境脚本未加载?)", zone)
				}
				env.FetchedAt = time.Now()
				return env, nil
			}
		case <-deadline:
			return nil, fmt.Errorf("回显 T100 环境变量超时")
		}
	}
}

// tenvProbeScript 拼出「加载 zone 环境并回读关键变量」的 bash 脚本。
// 与登录 profile 链同源:chenv(区域分支)→ topenv(TOP/ERP/COM)→ topsys(模块变量);
// 候选路径覆盖 UAT(/u3/pub)与金仓(/u1)两站点,source 失败静默跳过,最终以 echo 值为准。
// zone 含非法字符时返回空串(防注入)。
func tenvProbeScript(zone string) string {
	if !reZone.MatchString(zone) {
		return ""
	}
	z := strings.ReplaceAll(zone, "'", "'\\''")
	return fmt.Sprintf(`bash -lc '
ZONE=%s; export ZONE
source /u3/pub/bin/chenv %s >/dev/null 2>&1
source /u3/pub/etc/chenv >/dev/null 2>&1
source /u1/etc/chenv >/dev/null 2>&1
source /u3/pub/etc/topenv >/dev/null 2>&1
source /u1/etc/topenv >/dev/null 2>&1
source /u1/etc/topsys >/dev/null 2>&1
echo %s
echo TOP=$TOP
echo ERP=$ERP
echo COM=$COM
echo FGLDIR=$FGLDIR
echo FGLRESOURCEPATH=$FGLRESOURCEPATH
echo TOPENT=${TOPENT-}
echo %s
'`, z, z, TEnvBegin, TEnvEnd)
}

// probeTEnvScript 旧版 exec 通道探针(选项号直接当 ZONE 变量用,仅适用于菜单码与
// ZONE 值重合的站点,如 109 的 35/36;恒烁类站点菜单码≠ZONE 字符串,此路径会失败)。
// 保留作登录式探针失败时的兜底。
func probeTEnvScript(conn *SSHConn, zone string) (*RuntimeEnv, error) {
	script := tenvProbeScript(zone)
	if script == "" {
		return nil, fmt.Errorf("区域代码非法: %q", zone)
	}
	out, err := conn.Output(script, 30*time.Second)
	if err != nil && out == "" {
		return nil, fmt.Errorf("探测 T100 环境失败: %w", err)
	}
	var p TEnvParser
	for _, ln := range strings.Split(out, "\n") {
		if p.Feed(strings.TrimSpace(ln)) {
			break
		}
	}
	env := p.Env()
	env.FetchedAt = time.Now()
	if !env.Valid() {
		return nil, fmt.Errorf("未探测到 T100 环境变量(TOP 为空,区域 %s 环境脚本未加载?)", zone)
	}
	return env, nil
}
