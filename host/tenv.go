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
	FetchedAt       time.Time // 获取时间(缓存 TTL 判定)
}

// Valid 动态环境是否可用
func (e *RuntimeEnv) Valid() bool { return e != nil && e.TOP != "" && e.ERP != "" }

// reTEnvKV 匹配探针脚本回显的环境变量。TDICT_* 是本地写进远端 shell 的协议标记
// (脚本自己 echo 出来,不是远端已有变量),与 TDictCli 保持一致:改名无收益且必须真机验证。
var reTEnvKV = regexp.MustCompile(`^(TDICT_TOP|TDICT_ERP|TDICT_COM|TDICT_FGLDIR|TDICT_FGLRESOURCEPATH)=(\S*)\s*$`)

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
	// 4. 回显环境变量
	if err := pty.Write("echo TDICT_TOP=$TOP; echo TDICT_ERP=$ERP; echo TDICT_COM=$COM; echo TDICT_FGLDIR=$FGLDIR; echo TDICT_FGLRESOURCEPATH=$FGLRESOURCEPATH; echo TDICT_END\r"); err != nil {
		return nil, err
	}
	// 5. 解析回显
	env := &RuntimeEnv{}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ln, ok := <-lineCh:
			if !ok {
				return nil, fmt.Errorf("回显 T100 环境变量时连接已关闭")
			}
			ln = strings.TrimSpace(ln)
			if ln == "TDICT_END" {
				if !env.Valid() {
					return nil, fmt.Errorf("回显未包含 TOP/ERP(区域 %s 环境脚本未加载?)", zone)
				}
				env.FetchedAt = time.Now()
				return env, nil
			}
			if m := reTEnvKV.FindStringSubmatch(ln); m != nil {
				switch m[1] {
				case "TDICT_TOP":
					env.TOP = m[2]
				case "TDICT_ERP":
					env.ERP = m[2]
				case "TDICT_COM":
					env.COM = m[2]
				case "TDICT_FGLDIR":
					env.FGLDIR = m[2]
				case "TDICT_FGLRESOURCEPATH":
					env.FGLResourcePath = m[2]
				}
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
echo TDICT_TOP=$TOP
echo TDICT_ERP=$ERP
echo TDICT_COM=$COM
echo TDICT_FGLDIR=$FGLDIR
echo TDICT_FGLRESOURCEPATH=$FGLRESOURCEPATH
'`, z, z)
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
	env := &RuntimeEnv{FetchedAt: time.Now()}
	for _, ln := range strings.Split(out, "\n") {
		if m := reTEnvKV.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			switch m[1] {
			case "TDICT_TOP":
				env.TOP = m[2]
			case "TDICT_ERP":
				env.ERP = m[2]
			case "TDICT_COM":
				env.COM = m[2]
			case "TDICT_FGLDIR":
				env.FGLDIR = m[2]
			case "TDICT_FGLRESOURCEPATH":
				env.FGLResourcePath = m[2]
			}
		}
	}
	if !env.Valid() {
		return nil, fmt.Errorf("未探测到 T100 环境变量(TOP 为空,区域 %s 环境脚本未加载?)", zone)
	}
	return env, nil
}
