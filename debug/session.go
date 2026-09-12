package debug

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"tdebug/host"
	"time"

	"github.com/pkg/sftp"
)

// rawDebug 置 TDBG_RAW=1 时把协议原始行打到服务日志(排障用)
var rawDebug = os.Getenv("TDBG_RAW") == "1"

// State 会话状态
type State string

const (
	StateLoading State = "loading" // 正在建立(菜单/shell/启动 fglrun)或正在启动新一轮调试
	StateStopped State = "stopped" // 停在 (fgldb) 提示符,可发命令
	StateRunning State = "running" // 程序运行中,禁止发命令,仅可 \x03 中断
	StateIdle    State = "idle"    // 本轮调试已结束,宿主 shell 保留(单一常驻会话,可直接再启动)
	StateExit    State = "exit"    // 会话已断开(SSH 已释放)
)

// ErrNotStopped 运行期发命令被闸门拒绝
var ErrNotStopped = errors.New("程序运行中,禁止发送调试命令(仅允许中断)")

// Frame 调用栈帧
type Frame struct {
	Idx  int    `json:"idx"`
	Func string `json:"func"`
	File string `json:"file"`
	Line int    `json:"line"`
}

// SourceLine 停站上下文中的源码行
type SourceLine struct {
	Num   int    `json:"num"`
	Text  string `json:"text"`
	IsCur bool   `json:"isCur"`
}

// StopInfo 停站现场
type StopInfo struct {
	Reason string       `json:"reason"` // entry|breakpoint|interrupt|step|finish|unknown
	BPNum  int          `json:"bpNum,omitempty"`
	Func   string       `json:"func,omitempty"`
	File   string       `json:"file,omitempty"`
	Line   int          `json:"line,omitempty"`
	Frames []Frame      `json:"frames,omitempty"`
	Source []SourceLine `json:"source,omitempty"`
}

// Breakpoint 断点
type Breakpoint struct {
	Num     int    `json:"num"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Func    string `json:"func,omitempty"`
	Enabled bool   `json:"enabled"`
	Note    string `json:"note,omitempty"` // 重复下断等场景的说明(返回既有断点时非空)
}

// VarItem 变量求值项(局部变量/自动变量)
type VarItem struct {
	Expr  string `json:"expr"`
	Value string `json:"value,omitempty"`
}

// VarDecl 全局变量声明项(info variables 输出名字+类型)
type VarDecl struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Event 会话事件(WS/MCP 广播)
type Event struct {
	Type      string    `json:"type"` // state|stopped|output|watchdog|log|dead|autovars|ai_action
	SessionID string    `json:"sessionId"`
	Time      time.Time `json:"time"`
	State     string    `json:"state,omitempty"`
	Stop      *StopInfo `json:"stop,omitempty"`
	Vars      []VarItem `json:"vars,omitempty"`
	Text      string    `json:"text,omitempty"`
}

// execResult 命令响应
type execResult struct {
	Cmd        string
	Lines      []string
	Stop       *StopInfo
	Frames     []Frame
	Value      string
	BP         *Breakpoint
	BPs        []Breakpoint
	Continuing bool
	SawShell   bool
	Err        string // 调试器错误行(No stack / No symbol 等)
}

type pendingCmd struct {
	cmd     string
	kind    string // where|print|break|breakpoints|step|continue|run|other
	mode    int    // waitPrompt|waitMarker|waitQuiet
	lines   []string
	value   strings.Builder
	valueOn bool
	frames  []Frame
	bps     []Breakpoint
	errText string
	res     chan *execResult

	valueTrunc bool // print 值已截断
	linesTrunc bool // 行列表已截断
}

const (
	maxPendingLines = 20000     // 单命令响应行数上限(容纳 info variables/functions)
	maxValueBytes   = 256 << 10 // 单个 print 值字节上限
)

// writeValue 追加 print 值(带截断保护)
func (p *pendingCmd) writeValue(str string) {
	if p.value.Len() >= maxValueBytes {
		if !p.valueTrunc {
			p.valueTrunc = true
			p.value.WriteString("\n[输出已截断]")
		}
		return
	}
	p.value.WriteString(str)
}

// addLine 追加响应行(带截断保护)
func (p *pendingCmd) addLine(ln string) {
	if len(p.lines) >= maxPendingLines {
		if !p.linesTrunc {
			p.linesTrunc = true
			p.lines = append(p.lines, "[输出已截断]")
		}
		return
	}
	p.lines = append(p.lines, ln)
}

const (
	waitPrompt = iota // 等裸 (fgldb) 提示符
	waitMarker        // 等 "Continuing."(continue)
	waitQuiet         // 等输出安静(run)
)

type stopCollect struct {
	reason string
	bpnum  int
	fn     string
	file   string
	line   int
	frames []Frame
	source []SourceLine
}

// Session 一条调试会话 = 一条 SSH PTY 里跑的 fglrun -d
type Session struct {
	ID      string
	Module  string
	Prog    string
	RunProg string // gzzz_t 解析出的实体程序编号(gendbg 语义);空 = 与 Prog 相同
	// gendbg 原版启动引用:gzza004 去 "$FGLRUN" 前缀(如 "$CINi/ainq120_wf"),
	// 启动命令原样交给选区后的 shell,$变量 展开为权威 42r 路径(标准/客制由环境决定);空 = 回退本地探测
	LaunchRef    string
	ExtraArgs    string // gzzz004 额外参数(gendbg 拼在程序名之后)
	ArgsOverride string // 启动参数覆盖(接口日志重放调试:报文文件对);空 = 用 LaunchArgs 模板

	cfg  *Config
	conn *host.SSHConn
	pty  *host.PTYSession

	mu       sync.Mutex
	state    State
	started  bool // 程序是否已 run 过(入口停站时步进类命令非法)
	pending  *pendingCmd
	collect  *stopCollect
	cur      StopInfo
	curFrame int // 当前选中的栈帧(frame N),新停站自动回到栈顶
	bps      map[int]*Breakpoint
	quitReq  bool
	startAt  time.Time // 进入 stopped 时刻(看门狗/停留计时)
	quitting bool

	lastAutovars []VarItem // 最近一次停站的自动变量求值结果
	autovarsOn   bool      // 停站后自动求值自动变量(前端面板开关,默认关:减少自动调度拖慢命令队列)

	emit  func(Event)
	lines chan string // 全量行广播(启动序列等待用)
	done  chan struct{}

	sftpClientCache *sftp.Client
	srcCache        map[string]srcCacheEntry

	stopTimer *time.Timer
	kaStop    func() // 停止 SSH 心跳(主动关闭连接前调用,避免误报掉线)
	restoring bool   // 入口停站后的断点恢复进行中(对外仍报 loading)

	custModule string // 转客制作业:实际启动的客制模块名(cpm/apm→cpm),空 = 标准模块

	// ---- 单一常驻会话(复用宿主) ----
	envName        string // 会话所连环境名(设置页 envs 名称;空 = 顶层默认/推导名)
	booted         bool   // 是否已完成首次登录(菜单→shell),宿主可复用
	shellReady     bool   // pty 当前停在 shell 提示符,可直接敲 shell 命令
	topentOverride string // 会话内手动设置的 TOPENT(空 = 未设置,按配置默认)
}

// NewSession 建立 SSH 连接并打开 PTY(登录与启动由 Launch 驱动)。
// cfg 做一份快照:会话跨多轮调试存在,设置热更新不应改变其连接目标与启动参数
// (启动一轮时 Manager 会按最新生效配置重刷,见 SetRun)。
func NewSession(cfg *Config, module, prog, runProg, launchRef, extraArgs string, emit func(Event)) (*Session, error) {
	conn, err := host.Dial(cfg.SSH)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	cc := *cfg
	s := &Session{
		ID:        fmt.Sprintf("s%d", time.Now().UnixMilli()),
		Module:    module,
		Prog:      prog,
		RunProg:   runProg,
		LaunchRef: launchRef,
		ExtraArgs: extraArgs,
		cfg:       &cc,
		conn:      conn,
		state:     StateLoading,
		envName:   cfg.EnvName(),
		bps:       map[int]*Breakpoint{},
		srcCache:  map[string]srcCacheEntry{},
		emit:      emit,
		lines:     make(chan string, 1024),
		done:      make(chan struct{}),
	}
	s.kaStop = conn.StartKeepalive(func(err error) {
		s.emitEvent(Event{Type: "dead", Text: "SSH 连接断开: " + err.Error()})
		s.forceExit()
	})
	pty, err := conn.NewPTY(cfg.TermWidth, cfg.TermHeight)
	if err != nil {
		s.kaStop()
		conn.Close()
		return nil, fmt.Errorf("打开 PTY 失败: %w", err)
	}
	s.pty = pty
	go s.pump()
	return s, nil
}

// ---------- 生命周期 ----------

// runProgName 取本轮的实体程序名(优先 RunProg,回退 Prog)
func (s *Session) runProgName() string {
	if s.RunProg != "" {
		return s.RunProg
	}
	return s.Prog
}

// EnvName 返回会话所属环境名
func (s *Session) EnvName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.envName
}

// sameTarget 判断另一配置是否指向同一宿主(host/port/user/zone 全同)
func (s *Session) sameTarget(cfg *Config) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg == nil || s.cfg == nil {
		return false
	}
	a, b := s.cfg.SSH, cfg.SSH
	return a.Host == b.Host && a.Port == b.Port && a.User == b.User && s.cfg.Zone == cfg.Zone
}

// Launch 启动一轮调试(单一常驻会话):
// - 首次调用:先完成登录(区域菜单 → shell → 回读运行时环境),之后宿主常驻;
// - 后续调用:复用已登录宿主,直接从「进目录 → fglrun」开始,免去重复登录;
// 一轮结束只回到 idle(enterIdle),SSH/登录态保留;失败则尽力把宿主收回空闲。
func (s *Session) Launch(ctx context.Context) error {
	s.beginRun()
	err := s.launchInner(ctx)
	if err != nil && s.booted {
		// 复用宿主上启动失败(常见:作业名错误 fglrun 未起):回收宿主到空闲,
		// 不销毁连接——避免失败一次就重新登录;真卡死由用户用「重启会话」重建
		s.enterIdle("启动失败,会话保留(可换作业重试或重启会话): " + err.Error())
	}
	return err
}

// launchInner 启动流程主体(见 Launch 说明)
func (s *Session) launchInner(ctx context.Context) error {
	launchProg := s.runProgName()
	var resolveErr error

	// 模块解析:已有动态路径(复用宿主登录环境/探测缓存)先试;登录后再用权威环境补一次
	if s.Module == "" && s.cfg.Runtime != nil && s.cfg.Runtime.Valid() {
		if mod, err := s.resolveModule(launchProg); err == nil {
			s.setResolvedModule(launchProg, mod)
		} else {
			resolveErr = err
		}
	}

	if !s.booted {
		// 预登录解析(探针缓存动态路径,可能尚不可用);失败不中断——登录后真实环境再试
		if s.Module == "" {
			if mod, err := s.resolveModule(launchProg); err == nil {
				s.setResolvedModule(launchProg, mod)
			} else {
				resolveErr = err
			}
		}
		if err := s.bootHost(ctx); err != nil {
			return err
		}
		// 登录后补一次模块解析:选区后的 TOP/ERP/COM 最权威,预登录探测不可靠时
		// (如区域与服务器菜单映射不一致)在这里用真实环境纠正
		if s.Module == "" && s.cfg.Runtime != nil && s.cfg.Runtime.Valid() {
			if mod, err := s.resolveModule(launchProg); err == nil {
				s.setResolvedModule(launchProg, mod)
			} else {
				resolveErr = err
			}
		}
	} else if !s.shellReady {
		if err := s.ensureShellForRun(); err != nil {
			return err
		}
	}

	if s.Module == "" {
		if resolveErr == nil {
			resolveErr = fmt.Errorf("在各模块 42r 目录中未找到作业 %s(请检查作业名)", launchProg)
		}
		return fmt.Errorf("自动解析模块失败: %w", resolveErr)
	}
	return s.startRun(ctx, launchProg)
}

// setResolvedModule 记录模块解析结果并广播日志
func (s *Session) setResolvedModule(prog, mod string) {
	s.Module = mod
	s.emitEvent(Event{Type: "log", Text: fmt.Sprintf("按作业名解析模块:%s → %s", prog, mod)})
}

// beginRun 开启新一轮调试前的复位:清上一轮现场,置 loading(宿主可能复用)。
// 注意:不动 booted/shellReady(宿主登录态跨轮保留)。
func (s *Session) beginRun() {
	s.mu.Lock()
	prev := s.state
	s.state = StateLoading
	s.started = false
	s.pending = nil
	s.collect = nil
	s.quitReq = false
	s.cur = StopInfo{}
	s.curFrame = 0
	s.bps = map[int]*Breakpoint{}
	s.lastAutovars = nil
	s.restoring = false
	s.custModule = ""
	s.mu.Unlock()
	s.stopWatchdog()
	if prev != StateLoading {
		s.emitEvent(Event{Type: "state", State: string(StateLoading)})
	}
}

// bootHost 首次登录:区域菜单 → 选区 → shell → 回读运行时环境 → 下发环境默认 TOPENT。
// 仅调用一次。
func (s *Session) bootHost(ctx context.Context) error {
	// 1. 等区域菜单
	// 菜单格式两种:109 那台 `(*)Exit`,金仓这台 `*)Exit`(无左括号)
	if err := s.waitRegexp(host.ReLoginMenu, 25*time.Second, "区域菜单"); err != nil {
		return err
	}
	s.pty.Write(s.cfg.Zone + "\r")
	// 2. 等 shell 提示符
	if err := s.waitRegexp(host.ReShellPrompt, 25*time.Second, "shell 提示符"); err != nil {
		return err
	}
	s.markShellReady()
	// 2.5 连接即下发环境默认 TOPENT(配置企业;会话内已有手动覆盖时用覆盖值)。
	// 选区只给机器默认(日志里那行 TOPENT = 99),配置里的企业原本要等下一轮调试启动才
	// export,会让"刚连上的会话"与设置页不一致 —— 故登录后立即下发,会话企业从一开始就是配置值。
	// 空值不下发(保持选区登录默认),与 topentForRun 语义一致。
	// 特意放在回读之前:回读到的 TOPENT 就是下发后的当前真实值,界面不必自行推断。
	if ent := s.topentForRun(); ent != "" {
		if err := s.applyTopentToShell(ent); err != nil {
			return fmt.Errorf("登录 %s(环境 %s)后下发默认 TOPENT=%q 失败: %w",
				s.cfg.SSH.Host, s.envName, ent, err)
		}
	}
	// 2.6 回读登录后的权威 T100 路径(选区后环境变量,与标准 debug 同源)。
	// T100 路径只来自登录动态获取:回读失败即登录失败,无静态配置可回退
	if err := s.readRuntimeEnv(); err != nil {
		return fmt.Errorf("登录 %s(环境 %s,zone %s)后回读 T100 路径失败: %w",
			s.cfg.SSH.Host, s.envName, s.cfg.Zone, err)
	}
	s.mu.Lock()
	s.booted = true
	s.mu.Unlock()
	s.emitEvent(Event{Type: "log", Text: fmt.Sprintf("已登录 %s(环境 %s),会话将常驻复用", s.cfg.SSH.Host, s.envName)})
	return nil
}

// ensureShellForRun 复用宿主开跑前,确保 pty 当前停在可用的 shell 提示符:
// 若上一轮结束在 (fgldb) 残留(fglrun 未随程序退出),先补发 quit 回到 shell。
func (s *Session) ensureShellForRun() error {
	s.mu.Lock()
	if s.shellReady {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	s.emitEvent(Event{Type: "log", Text: "上一轮调试器未完全退出,先回到 shell"})
	deadline := time.After(8 * time.Second)
	for {
		select {
		case ln := <-s.lines:
			if host.ReShellPrompt.MatchString(ln) {
				s.markShellReady()
				return nil
			}
			if host.IsBarePrompt(ln) {
				if err := s.pty.Write("quit\r"); err != nil {
					return err
				}
				if err := s.waitRegexp(host.ReShellPrompt, 20*time.Second, "shell 提示符(退出残留调试器)"); err != nil {
					return err
				}
				s.markShellReady()
				return nil
			}
		case <-deadline:
			// 没有提示符可循:保守按已停在 shell 处理(直接敲命令,若真卡住由超时回落)
			s.markShellReady()
			return nil
		}
	}
}

// startRun 运行启动段:进模块目录 → fglrun -d → 入口停站就绪。
// 前提:已登录(首次 bootHost 或复用),shellReady 为真,Module 已解析。
func (s *Session) startRun(ctx context.Context, launchProg string) error {
	// 3. 进模块目录 + 源码路径
	// 转客制的作业 42r/源码部署在 c<module> 目录(原版 T100 从 c** 启动,FGLLDPATH 也 c 优先),
	// 这里探测:客制目录有同名 42r 就用客制,否则用标准模块目录
	modDir := s.pickLaunchDir(launchProg)
	log.Printf("[debug] 会话 %s 启动目录: %s (prog=%s module=%s)", s.ID, modDir, launchProg, s.Module)
	setup := fmt.Sprintf("cd %s\r\nexport FGLSOURCEPATH=%s\r\n",
		modDir, s.fglsourcePathOf(modDir))
	if s.cfg.FGLServer != "" {
		setup += "export FGLSERVER=" + s.cfg.FGLServer + "\r\n"
	}
	// TOPENT:会话内手动设置优先,其次配置企业(ENT);都没有则不导出,沿用选区登录默认
	// (选区输出的是机器默认值如「TOPENT = 99」;作业运行/数据库连接都以 TOPENT 为准)
	// 连接时(bootHost)已下发过一次,这里再下发是为了覆盖"会话开着时在设置页改了配置"的情况。
	if ent := s.topentForRun(); ent != "" {
		// 单引号包裹并剔除内嵌单引号(值不限文本,防注入/拆词),与 topentShellCmd 同规则
		setup += "export TOPENT='" + strings.ReplaceAll(ent, "'", "") + "'\r\n"
		// 本轮起 shell 里的 TOPENT 即 ent:同步运行时回读值,界面显示的"当前会话 TOPENT"才不滞后
		s.mu.Lock()
		if s.cfg.Runtime != nil {
			s.cfg.Runtime.Topent = ent
		}
		s.mu.Unlock()
	}
	s.pty.Write(setup)
	if err := s.waitRegexp(host.ReShellPrompt, 15*time.Second, "shell 提示符(cd)"); err != nil {
		return err
	}
	// 4. 启动调试(launchProg 为 gzzz_t 解析出的实体程序;{prog} 仍传作业编号,与 gendbg 一致;
	//    接口重放调试时 ArgsOverride = "'<req>' '<rsp>'" 报文文件对)
	args := strings.ReplaceAll(s.cfg.LaunchArgs, "{prog}", s.Prog)
	if s.ArgsOverride != "" {
		args = s.ArgsOverride
	}
	if s.ExtraArgs != "" {
		args += " " + s.ExtraArgs // gzzz004 额外参数(gendbg 语义:拼在程序名后)
	}
	// 启动引用:gzza004 原版优先(如 "$CINi/ainq120_wf"),$变量由选区后 shell 展开为
	// 权威路径——标准/客制由 T100 环境决定,与 r.d/gendbg 完全同源;无引用才回退本地探测
	var runCmd string
	if s.LaunchRef != "" {
		runCmd = fmt.Sprintf("fglrun -d %s.42r %s", s.LaunchRef, args)
	} else {
		runCmd = fmt.Sprintf("fglrun -d 42r/%s.42r %s", launchProg, args)
	}
	log.Printf("[debug] 会话 %s 启动: %s", s.ID, runCmd)
	s.mu.Lock()
	s.shellReady = false // 离开 shell 进入 fglrun/程序
	s.mu.Unlock()
	s.pty.Write(runCmd + "\r")
	if err := s.waitBarePrompt(90*time.Second, "(fgldb) 提示符"); err != nil {
		return err
	}
	// 置停站态但暂不宣告:断点恢复要在提示符上发命令(exec 依赖 stopped 态),
	// 恢复期间 State() 对外仍报 loading,恢复完才发 stopped 事件。否则前端见到
	// stopped 即停止轮询,首帧快照里断点还是空的,要步进一次才能看到缓存断点
	s.mu.Lock()
	s.state = StateStopped
	s.startAt = time.Now()
	s.restoring = true
	s.mu.Unlock()
	s.setStop(s.entryStopInfo())
	// print 元素上限(防 T100 大数组 print 刷爆输出,fgldeb 同款防护)
	if _, err := s.exec("other", fmt.Sprintf("set print elements %d", s.cfg.PrintElements), waitPrompt, 10*time.Second); err != nil {
		s.emitEvent(Event{Type: "log", Text: "set print elements 失败(不影响使用): " + err.Error()})
	}
	s.restoreBreakpoints()
	s.mu.Lock()
	s.restoring = false
	s.mu.Unlock()
	s.emitEvent(Event{Type: "state", State: string(StateStopped)})
	s.emitEvent(Event{Type: "log", Text: "调试会话就绪: " + s.Prog + "@" + s.cfg.Zone})
	return nil
}

// markShellReady 标记 pty 已停在 shell 提示符
func (s *Session) markShellReady() {
	s.mu.Lock()
	s.shellReady = true
	s.mu.Unlock()
}

// Booted 宿主是否已完成首次登录(可复用)
func (s *Session) Booted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.booted
}

// CloneCfg 返回会话连接配置的快照(同环境重启/重连用)
func (s *Session) CloneCfg() *Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	cc := *s.cfg
	return &cc
}

// BootIdle 仅完成登录并回到空闲(不启动调试)——会话「重启/切换/连接」用
func (s *Session) BootIdle(ctx context.Context) error {
	if !s.booted {
		if err := s.bootHost(ctx); err != nil {
			return err
		}
	}
	s.enterIdle("会话已就绪(空闲),可直接启动调试")
	return nil
}

// SetRun 复用宿主的下一轮运行参数(由 Manager 在复用启动前按最新生效配置调用)
func (s *Session) SetRun(cfg *Config, module, prog, runProg, launchRef, extra string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg != nil {
		cc := *cfg
		cc.Runtime = s.cfg.Runtime // 会话内动态路径保留(同一宿主/区域)
		s.cfg = &cc
	}
	s.Module = module
	s.Prog = prog
	s.RunProg = runProg
	s.LaunchRef = launchRef
	s.ExtraArgs = extra
	s.ArgsOverride = ""
	s.custModule = ""
}

// TopentOverride 会话内手动设置的 TOPENT(空 = 未设置,按配置默认)
func (s *Session) TopentOverride() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.topentOverride
}

// TopentCfg 配置级企业 TOPENT(设置页 SSH 页 topent;未手动设置时运行生效值)
func (s *Session) TopentCfg() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(string(s.cfg.Topent))
}

// TopentShell 登录后 shell 里实际回读到的 $TOPENT(选区/环境脚本给的企业;空 = 登录未导出)。
// 与 TopentCfg 的区别:连接会话本身不 export TOPENT,只有下一轮调试启动才 export
// override/配置值,所以"当前连的是哪个企业"以本值为准,供界面展示给用户参考。
func (s *Session) TopentShell() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Runtime == nil {
		return ""
	}
	return strings.TrimSpace(s.cfg.Runtime.Topent)
}

// topentForRun 本轮运行生效的 TOPENT:会话内手动设置优先,其次配置企业(TOPENT),再否则不导出(沿用登录默认)
func (s *Session) topentForRun() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.topentOverride != "" {
		return s.topentOverride
	}
	return strings.TrimSpace(string(s.cfg.Topent))
}

// SetTopent 空闲态重新设置宿主 shell 的 TOPENT(空值 = 清除手动覆盖,回到环境默认 TOPENT)。
// 立即 export 到 shell(清除时下发环境默认值,环境也没配才 unset),随后的各轮调试仍按
// topentForRun 优先导出覆盖值。
func (s *Session) SetTopent(value string) error {
	s.mu.Lock()
	s.topentOverride = value
	s.mu.Unlock()
	// 清除覆盖(空值)不能简单 unset:连接时已把环境默认下发进 shell,unset 会掉回选区机器默认,
	// 与"会话企业 = 设置页配置"不一致。这里直接下发 topentForRun()(覆盖值 || 配置企业;
	// 都没有才是空 = unset),让宿主 shell 的 TOPENT 恒等于当前生效值。
	eff := s.topentForRun()
	if err := s.applyTopentToShell(eff); err != nil {
		return err
	}
	if value == "" {
		if eff != "" {
			s.emitEvent(Event{Type: "log", Text: fmt.Sprintf("已清除 TOPENT 覆盖,回到环境默认 %q", eff)})
		} else {
			s.emitEvent(Event{Type: "log", Text: "已清除 TOPENT 覆盖(环境未配置,shell 内已 unset)"})
		}
		return nil
	}
	s.emitEvent(Event{Type: "log", Text: fmt.Sprintf("TOPENT 已设置为 %q(会话内,立即生效)", value)})
	return nil
}

// applyTopentToShell 把 value 直接 export/unset 到宿主 shell(空值 = 清除)。
// 只动 shell、不记录 override:SetTopent(会话内手动设置)与 bootHost(连接时下发环境默认)共用。
// 回执超时只记日志不报错(命令通常已生效);写入终端失败才返回错误。
func (s *Session) applyTopentToShell(value string) error {
	if err := s.pty.Write(topentShellCmd(value)); err != nil {
		return fmt.Errorf("写入终端失败: %w", err)
	}
	// 等待 shell 回执(确认命令已被执行)
	if err := s.waitRegexp(reTopentOK, 5*time.Second, "TOPENT 设置回执"); err != nil {
		s.emitEvent(Event{Type: "log", Text: "TOPENT 已下发但未等到 shell 回执: " + err.Error()})
	}
	// shell 里的 TOPENT 已变成 value(或已 unset):同步运行时回读值,
	// 使界面显示的"当前会话 TOPENT"就是 shell 真实值,而不是登录那一刻的旧值。
	s.mu.Lock()
	if s.cfg.Runtime != nil {
		s.cfg.Runtime.Topent = value
	}
	s.mu.Unlock()
	return nil
}

// topentShellCmd 拼出把 TOPENT 下发到宿主 shell 的命令(空值 = unset)。
// 值不限数字/文本:shell 单引号包裹并剔除内嵌单引号,防注入/拆词。
func topentShellCmd(value string) string {
	if value == "" {
		return "unset TOPENT; echo " + reTopentOKMark + "\r"
	}
	q := "'" + strings.ReplaceAll(value, "'", "") + "'"
	return "export TOPENT=" + q + "; echo " + reTopentOKMark + "\r"
}

// reTopentOKMark TOPENT 下发回执标记(TDBG 是本工具自己的本地标记,非远端变量名)
const reTopentOKMark = "TDBG-TOPENT-OK"

// reTopentOK 回执标记:必须匹配独占一行的回执。
// PTY 会把我方发去的整条命令原样回显,该行同样含有本标记字符串 ——
// 用子串匹配会在命令被 shell 执行之前就"等到回执",锚定整行才能排除命令回显本身。
var reTopentOK = regexp.MustCompile(`^\s*` + reTopentOKMark + `\s*$`)

// enterIdle 把会话置为空闲(宿主 shell 保留、无调试运行)并广播 state=idle。
// 程序自然退出 / 结束调试 / 复用启动失败都会回到这个状态。
func (s *Session) enterIdle(logMsg string) {
	s.mu.Lock()
	if s.state == StateExit {
		s.mu.Unlock()
		return
	}
	wasIdle := s.state == StateIdle
	s.state = StateIdle
	s.pending = nil
	s.collect = nil
	s.quitReq = false
	s.cur = StopInfo{}
	s.curFrame = 0
	s.lastAutovars = nil
	s.mu.Unlock()
	s.stopWatchdog()
	if !wasIdle {
		s.emitEvent(Event{Type: "state", State: string(StateIdle)})
	}
	if logMsg != "" {
		s.emitEvent(Event{Type: "log", Text: logMsg})
	}
}

// reProgName 作业名白名单(用于拼 shell 命令,防注入)
var reProgName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// pickLaunchDir 决定启动目录:客制目录 42r 有同名 42r 用客制,否则标准模块目录。
// 与原版 T100 一致:转客制作业从 c** 启动,客制目录 = 模块首字母 a→c(ain→cin、apm→cpm)
func (s *Session) pickLaunchDir(prog string) string {
	modDir := s.cfg.ModuleDir(s.Module)
	if s.Module == "" || !reProgName.MatchString(s.Module) || !strings.HasPrefix(s.Module, "a") || !strings.HasSuffix(modDir, "/"+s.Module) {
		return modDir
	}
	cust := strings.TrimSuffix(modDir, "/"+s.Module) + "/c" + s.Module[1:]
	out, _ := s.conn.Output(fmt.Sprintf("ls %s/42r/%s.42r 2>/dev/null", cust, prog), 10*time.Second)
	if strings.Contains(out, prog+".42r") {
		s.mu.Lock()
		s.custModule = "c" + s.Module[1:] // 客制模块名(apm→cpm):DVM 停站报的就是它
		s.mu.Unlock()
		s.emitEvent(Event{Type: "log", Text: fmt.Sprintf("转客制作业:使用客制目录 %s", cust)})
		return cust
	}
	return modDir
}

// entryStopInfo 构造入口停站:带上实际启动的源文件(标准/客制),让前端入口就加载
// 与 DVM 一致的文件,第一次步进不再跨文件重载
func (s *Session) entryStopInfo() *StopInfo {
	si := &StopInfo{Reason: "entry"}
	mod := s.Module
	if mod == "" || !reProgName.MatchString(mod) || s.RunProg == "" {
		return si
	}
	if s.custModule != "" {
		si.File = s.custModule + "_" + s.RunProg + ".4gl" // cpm_apmt520_wf.4gl
	} else {
		si.File = mod + "_" + s.RunProg + ".4gl" // apm_apmt520_wf.4gl
	}
	return si
}

// readRuntimeEnv 在选区后的 shell 里回显关键 T100 环境变量并写入 cfg.Runtime。
// 选区后是真实登录环境(与标准 debug 完全同源),是路径的最权威来源;
// 失败由调用方(bootHost)作为登录失败报错——T100 路径无静态配置可回退。
// 探针与解析共用 host 层的 TEnvProbe/TEnvParser:定界符之间只回显真实变量名,
// 且只有区间内的取值行才被采纳(命令回显与终端重绘碎片都不会污染取值)。
func (s *Session) readRuntimeEnv() error {
	if err := s.pty.Write(host.TEnvProbe() + "\r"); err != nil {
		return err
	}
	var p host.TEnvParser
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ln := <-s.lines:
			if p.Feed(strings.TrimSpace(ln)) {
				env := p.Env()
				if !env.Valid() {
					return fmt.Errorf("未回显到 TOP/ERP(环境脚本未设置 $TOP?)")
				}
				env.FetchedAt = time.Now()
				s.mu.Lock()
				s.cfg.Runtime = env
				s.mu.Unlock()
				return nil
			}
		case <-deadline:
			return fmt.Errorf("回读 T100 环境变量超时")
		}
	}
}

// fglsourcePathOf 按实际启动目录拼源码搜索路径(公共库仍在 TOP/com 下,TOP 为登录动态获取)
func (s *Session) fglsourcePathOf(dir string) string {
	top := s.cfg.TopDirActual()
	dirs := []string{
		dir + "/4gl",
		dir + "/42m",
		top + "/com/lib/42m",
		top + "/com/sub/42m",
		top + "/com/qry/42m",
	}
	out := ""
	for i, d := range dirs {
		if i > 0 {
			out += ":"
		}
		out += d
	}
	return out
}

// resolveModule 按程序名在登录区源码目录各模块的 42r 目录中搜索 <prog>.42r:
// 唯一命中 → 返回模块;标准/客制(ain/cin)并存 → 选客制(转客制后原版从 c** 目录启动);
// 其余多命中 → 报出候选;无命中 → 报错
func (s *Session) resolveModule(prog string) (string, error) {
	if !reProgName.MatchString(prog) {
		return "", fmt.Errorf("作业名含非法字符: %q", prog)
	}
	found := searchModule42r(s.conn, s.cfg.ModuleRootsActual(), prog)
	switch {
	case len(found) == 0:
		// 防御:会话登录(bootHost)已保证动态环境解析成功;此处仅兜底提示
		if s.cfg.Runtime == nil || !s.cfg.Runtime.Valid() {
			return "", fmt.Errorf("会话缺少登录后动态解析的 T100 路径(环境 %s,zone %s),无法定位作业 %s;请重启会话后重试", s.envName, s.cfg.Zone, prog)
		}
		return "", fmt.Errorf("在各模块 42r 目录中未找到作业 %s(请检查作业名)", prog)
	case len(found) > 1:
		// 标准模块与它的客制目录(首字母 a→c,如 ain/cin)并存视为同一作业,取客制
		for _, m := range found {
			if strings.HasPrefix(m, "c") {
				for _, std := range found {
					if "c"+std[1:] == m {
						return m, nil
					}
				}
			}
		}
		return "", fmt.Errorf("作业 %s 存在于多个模块(%s),请指定模块", prog, strings.Join(found, ", "))
	}
	return found[0], nil
}

// searchModule42r 在各模块根的 42r 目录中按 <prog>.42r 搜索,返回去重后的模块列表
// (模块根直挂的 42r 记为 "";ls 部分 glob 未命中返回非零退出码,不能当作失败,只按输出解析)
func searchModule42r(conn *host.SSHConn, roots []string, prog string) []string {
	if !reProgName.MatchString(prog) {
		return nil
	}
	var pats []string
	for _, root := range roots {
		pats = append(pats, root+"/*/42r/"+prog+".42r", root+"/42r/"+prog+".42r")
	}
	out, _ := conn.Output("ls -d "+strings.Join(pats, " ")+" 2>/dev/null", 20*time.Second)
	mods := map[string]bool{}
	var found []string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasSuffix(ln, "/42r/"+prog+".42r") {
			continue
		}
		parts := strings.Split(ln, "/")
		mod := ""
		for i, p := range parts {
			if p == "42r" && i > 0 {
				mod = parts[i-1]
				break
			}
		}
		if mod == "erp" || mod == "com" {
			mod = "" // 模块根直挂的 42r
		}
		if !mods[mod] {
			mods[mod] = true
			found = append(found, mod)
		}
	}
	return found
}

// modHas42r 校验 <topDir>/erp/<mod>/42r/<prog>.42r 是否存在
func modHas42r(conn *host.SSHConn, topDir, mod, prog string) bool {
	if mod == "" || !reProgName.MatchString(mod) || !reProgName.MatchString(prog) {
		return false
	}
	out, _ := conn.Output(
		fmt.Sprintf("ls %s/erp/%s/42r/%s.42r 2>/dev/null", topDir, mod, prog), 10*time.Second)
	return strings.Contains(out, prog+".42r")
}

// EndRun 结束本轮调试,保留宿主会话(回到 idle):
// 运行中先中断拿回控制权,再向 fgldb 发 quit 回到 shell,连接不释放。
// 若拿不回控制权(卡死)或状态异常,降级为整体断开 Close()。
func (s *Session) EndRun() error {
	s.mu.Lock()
	if s.quitting || s.state == StateExit || s.state == StateIdle {
		s.mu.Unlock()
		return nil
	}
	st := s.state
	s.mu.Unlock()

	if st == StateRunning {
		_ = s.Interrupt()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if s.State() == StateStopped {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	if s.State() != StateStopped {
		// running 拿不回控制权 / loading 启动中:没有可保留的干净宿主,整体断开
		return s.Close()
	}
	r, err := s.exec("other", "quit", waitPrompt, 20*time.Second)
	if err != nil || !r.SawShell {
		// quit 未正常回到 shell(状态不可靠):整体断开兜底
		if err == nil {
			err = fmt.Errorf("quit 后未回到 shell")
		}
		s.emitEvent(Event{Type: "log", Text: "结束本轮调试异常,改为断开会话: " + err.Error()})
		return s.Close()
	}
	s.markShellReady()
	s.enterIdle("结束调试:本轮运行已结束,会话保留(可直接再次启动)")
	return nil
}

// Close 彻底结束会话并释放 SSH(切换环境/重启会话/服务退出/异常兜底用):
// 必要时先中断,再向 fgldb quit,最后关闭连接。
func (s *Session) Close() error {
	s.mu.Lock()
	if s.quitting {
		s.mu.Unlock()
		return nil
	}
	s.quitting = true
	st := s.state
	s.mu.Unlock()

	if st == StateRunning {
		s.Interrupt()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if s.State() == StateStopped {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	if s.State() == StateStopped {
		s.exec("other", "quit", waitPrompt, 20*time.Second)
	}
	s.forceExit()
	return nil
}

// Interrupt 运行中发 SIGINT 拿回控制权
func (s *Session) Interrupt() error {
	s.mu.Lock()
	st := s.state
	started := s.started
	s.mu.Unlock()
	if !started {
		return fmt.Errorf("程序尚未启动,无需中断:直接「继续」即可运行,或点行号下断点")
	}
	if st != StateRunning {
		return fmt.Errorf("仅运行中可中断(当前 %s)", st)
	}
	s.emitEvent(Event{Type: "log", Text: "发送 SIGINT 中断"})
	return s.pty.Write("\x03")
}

// ForceExit 供 API 层终结失败会话(置 exit 态并释放资源)
func (s *Session) ForceExit() { s.forceExit() }

// forceExit 强制进入退出态并释放资源
func (s *Session) forceExit() {
	s.mu.Lock()
	already := s.state == StateExit
	s.state = StateExit
	p := s.pending
	s.pending = nil
	s.mu.Unlock()
	if s.stopTimer != nil {
		s.stopTimer.Stop()
	}
	if p != nil {
		p.res <- &execResult{Cmd: p.cmd, SawShell: true}
	}
	if !already {
		s.emitEvent(Event{Type: "state", State: string(StateExit)})
		s.emitEvent(Event{Type: "log", Text: "会话已断开,SSH 连接已释放(下次启动将重新登录)"})
	}
	// 先停心跳再关连接:这条连接是本会话独占的,关闭属于正常释放而非掉线
	if s.kaStop != nil {
		s.kaStop()
	}
	s.pty.Close()
	s.conn.Close()
}

// ---------- 命令执行 ----------

// exec 在 STOPPED 态发送一条命令并等待响应。
// 若上一条命令仍在执行(如 autovars 后台求值),最多宽限 1.5s 再报忙。
func (s *Session) exec(kind, cmd string, mode int, timeout time.Duration) (*execResult, error) {
	busyDeadline := time.Now().Add(1500 * time.Millisecond)
	var p *pendingCmd
	for {
		s.mu.Lock()
		if s.state != StateStopped {
			st := s.state
			s.mu.Unlock()
			if st == StateRunning {
				return nil, ErrNotStopped
			}
			return nil, fmt.Errorf("当前状态 %s 不可发送命令", st)
		}
		if s.pending == nil {
			p = &pendingCmd{cmd: cmd, kind: kind, mode: mode, res: make(chan *execResult, 1)}
			s.pending = p
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
		if time.Now().After(busyDeadline) {
			return nil, fmt.Errorf("上一条命令仍在执行")
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := s.pty.Write(cmd + "\r"); err != nil {
		s.clearPending()
		return nil, fmt.Errorf("写入终端失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	select {
	case r := <-p.res:
		return r, nil
	case <-ctx.Done():
		s.clearPending()
		s.recoverAfterTimeout(cmd)
		return nil, fmt.Errorf("命令超时(%s): %s", timeout, cmd)
	}
}

// recoverAfterTimeout 命令超时后探测真实状态:发 SIGINT,
// 若回到 (fgldb) 提示符说明已停站;否则按运行中处理
func (s *Session) recoverAfterTimeout(cmd string) {
	s.emitEvent(Event{Type: "log", Text: "命令超时,发送中断探测状态: " + cmd})
	_ = s.pty.Write("\x03")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.State() == StateStopped {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	s.setState(StateRunning)
}

func (s *Session) clearPending() {
	s.mu.Lock()
	p := s.pending
	s.pending = nil
	s.mu.Unlock()
	if p != nil {
		select {
		case p.res <- &execResult{Cmd: p.cmd}:
		default:
		}
	}
}

// completePending 归还命令响应(由输出泵调用)
func (s *Session) completePending(r *execResult) {
	s.mu.Lock()
	p := s.pending
	s.pending = nil
	s.mu.Unlock()
	if p == nil {
		return
	}
	if r.Cmd == "" {
		r.Cmd = p.cmd
	}
	if len(r.Lines) == 0 {
		r.Lines = p.lines
	}
	select {
	case p.res <- r:
	default:
	}
}

// Continue 继续运行到下一停站
func (s *Session) Continue() (*execResult, error) {
	if !s.Started() {
		// 入口停站:程序尚未 run,fgldb pre-run 态 continue 非法("The program is not being run"),
		// 透明等效 run(从入口启动,跑到第一个停站),符合"点继续=让程序跑起来"的直觉
		return s.Run()
	}
	r, err := s.exec("continue", "continue", waitMarker, 30*time.Second)
	if err == nil {
		s.markStarted()
	}
	return r, err
}

// Run 从入口启动程序
func (s *Session) Run() (*execResult, error) {
	r, err := s.exec("run", "run", waitQuiet, 60*time.Second)
	if err == nil {
		s.markStarted()
	}
	return r, err
}

// markStarted 标记程序已启动(步进类命令此后才合法)
func (s *Session) markStarted() {
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
}

// Started 程序是否已 run 过
func (s *Session) Started() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

// Break 下断点(位置: 行号 / 函数名 / file:line)。
// 注意 fgldb 会把非可执行行的断点自动调整到下一条可执行语句,
// 返回的 bp.Line 才是真实生效位置(可能与请求行号不同)。
// 调整后与既有断点落在同一位置时,撤销新增并返回既有断点(带 Note)。
func (s *Session) Break(loc string) (*Breakpoint, error) {
	r, err := s.exec("break", "break "+loc, waitPrompt, 20*time.Second)
	if err != nil {
		return nil, err
	}
	var bp *Breakpoint
	for _, ln := range r.Lines {
		if m := reBPSet.FindStringSubmatch(ln); m != nil {
			num, _ := strconv.Atoi(m[1])
			line, _ := strconv.Atoi(m[3])
			bp = &Breakpoint{Num: num, File: m[2], Line: line, Enabled: true}
		}
	}
	if bp == nil {
		return nil, fmt.Errorf("断点设置失败: %s", strings.Join(r.Lines, " | "))
	}
	s.syncBreakpoints()
	// 重复防护:fgldb 调整行号后可能与既有断点撞在同一位置
	s.mu.Lock()
	var dup *Breakpoint
	for _, b := range s.bps {
		if b.Num != bp.Num && b.File == bp.File && b.Line == bp.Line {
			dup = b
			break
		}
	}
	s.mu.Unlock()
	if dup != nil {
		if err := s.DeleteBreakpoint(bp.Num); err != nil {
			return dup, fmt.Errorf("重复断点清理失败: %w", err)
		}
		dup.Note = fmt.Sprintf("该位置已有断点 #%d(%s:%d),未重复添加", dup.Num, dup.File, dup.Line)
		return dup, nil
	}
	s.persistBPs()
	return bp, nil
}

// DeleteBreakpoint 删除断点
func (s *Session) DeleteBreakpoint(num int) error {
	_, err := s.exec("other", fmt.Sprintf("delete %d", num), waitPrompt, 15*time.Second)
	if err == nil {
		s.syncBreakpoints()
		s.persistBPs()
	}
	return err
}

// SetBPEnabled 启用/禁用断点(enable/disable)
func (s *Session) SetBPEnabled(num int, enabled bool) error {
	cmd := "disable"
	if enabled {
		cmd = "enable"
	}
	if _, err := s.exec("other", fmt.Sprintf("%s %d", cmd, num), waitPrompt, 15*time.Second); err != nil {
		return err
	}
	s.syncBreakpoints()
	s.persistBPs()
	return nil
}

// syncBreakpoints 用 info breakpoints 重建断点账本(fgldb 为唯一真相,杜绝漂移)
func (s *Session) syncBreakpoints() {
	r, err := s.exec("other", "info breakpoints", waitPrompt, 15*time.Second)
	if err != nil {
		return
	}
	nbps := map[int]*Breakpoint{}
	for _, ln := range r.Lines {
		if m := reBPInfo.FindStringSubmatch(ln); m != nil {
			num, _ := strconv.Atoi(m[1])
			line, _ := strconv.Atoi(m[5])
			nbps[num] = &Breakpoint{Num: num, Func: m[3], File: m[4], Line: line, Enabled: m[2] == "y"}
		}
	}
	s.mu.Lock()
	s.bps = nbps
	s.mu.Unlock()
}

// Print 求值表达式
func (s *Session) Print(expr string) (string, error) {
	r, err := s.exec("print", "print "+expr, waitPrompt, 30*time.Second)
	if err != nil {
		return "", err
	}
	if r.Err != "" {
		return r.Err, fmt.Errorf("%s", r.Err)
	}
	return strings.TrimSpace(r.Value), nil
}

// Where 调用栈
func (s *Session) Where() ([]Frame, error) {
	r, err := s.exec("where", "where", waitPrompt, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if r.Err != "" {
		return nil, fmt.Errorf("%s", r.Err)
	}
	if len(r.Frames) == 0 && s.cur.Frames != nil {
		return s.cur.Frames, nil
	}
	return r.Frames, nil
}

// ---------- 上下文查询(fgldeb 同款命令面,格式来自 3.21.03 实测) ----------

// Locals 当前帧全部局部变量(info locals,`名字 = 值`,值可续行)。
// 入口停站/无局部变量时返回空列表。随 Frame(n) 切换上下文。
func (s *Session) Locals() ([]VarItem, error) {
	r, err := s.exec("other", "info locals", waitPrompt, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if r.Err != "" {
		return nil, fmt.Errorf("%s", r.Err)
	}
	var out []VarItem
	for _, ln := range r.Lines {
		if host.IsBarePrompt(ln) {
			continue
		}
		if m := reLocalVar.FindStringSubmatch(ln); m != nil {
			out = append(out, VarItem{Expr: m[1], Value: strings.TrimSpace(m[2])})
			continue
		}
		// 值跨行的续行(非 name=value 且非提示符)聚合到上一项
		if len(out) > 0 && strings.TrimSpace(ln) != "" {
			out[len(out)-1].Value += "\n" + ln
		}
	}
	return out, nil
}

// Globals 全部全局变量声明(info variables,实测格式:`Globals:` 头 + `名字 类型` 行)。
// T100 全局量可达数千行,limit<=0 时默认 4000;返回 (截取结果, 总数)。
func (s *Session) Globals(limit int) ([]VarDecl, int, error) {
	if limit <= 0 {
		limit = 4000
	}
	r, err := s.exec("other", "info variables", waitPrompt, 60*time.Second)
	if err != nil {
		return nil, 0, err
	}
	if r.Err != "" {
		return nil, 0, fmt.Errorf("%s", r.Err)
	}
	var out []VarDecl
	for _, ln := range r.Lines {
		if strings.TrimSpace(ln) == "" || host.IsBarePrompt(ln) || ln == "info variables" {
			continue // 空行/提示符/PTY 命令回显行
		}
		if m := reGlobalDecl.FindStringSubmatch(ln); m != nil {
			out = append(out, VarDecl{Name: m[1], Type: strings.TrimSpace(m[2])})
		}
		// 非 `名字 类型` 的行(如分节头)跳过
	}
	total := len(out)
	if total > limit {
		out = out[:limit]
	}
	return out, total, nil
}

// Sources 作业已加载符号的全部模块名(info sources,逗号分隔可换行)
func (s *Session) Sources() ([]string, error) {
	r, err := s.exec("other", "info sources", waitPrompt, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if r.Err != "" {
		return nil, fmt.Errorf("%s", r.Err)
	}
	var out []string
	seen := map[string]bool{}
	for _, ln := range r.Lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || host.IsBarePrompt(ln) {
			continue
		}
		if strings.HasSuffix(ln, ":") && strings.Contains(ln, "Source files") {
			continue // 分节头
		}
		for _, name := range strings.Split(ln, ",") {
			name = strings.TrimSpace(name)
			if name == "" || !strings.HasSuffix(name, ".4gl") || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out, nil
}

// Functions 全部函数名(info functions,每行 `name ()`;T100 大作业可达上万行)。
// limit<=0 时默认 5000;返回 (截取结果, 总数)。
func (s *Session) Functions(limit int) ([]string, int, error) {
	if limit <= 0 {
		limit = 5000
	}
	r, err := s.exec("other", "info functions", waitPrompt, 90*time.Second)
	if err != nil {
		return nil, 0, err
	}
	if r.Err != "" {
		return nil, 0, fmt.Errorf("%s", r.Err)
	}
	var out []string
	for _, ln := range r.Lines {
		if m := reFuncList.FindStringSubmatch(ln); m != nil {
			out = append(out, m[1])
		}
	}
	total := len(out)
	if total > limit {
		out = out[:limit]
	}
	return out, total, nil
}

// CalibrateOffset 行号校准:fgldb 报的行号来自 .42r 编译产物的行号表,若服务器上
// .4gl 源码与编译产物版本不一致(如文件头部增删行),DVM 行号会与磁盘文件整体偏移。
// 用停站源码块(行号+文本)在磁盘文件中反查真实行号,返回 offset(DVM 行号 = 磁盘行号 + offset)。
// offset > 0 时前端在源码顶部前插 offset 个空行,即可让 Monaco 行号与协议流对齐。
func (s *Session) CalibrateOffset() (int, error) {
	if s.State() != StateStopped {
		return 0, fmt.Errorf("需停站后才能校准(当前未停站)")
	}
	st := s.Cur()
	if st.File == "" {
		return 0, fmt.Errorf("当前停站缺少源文件信息(如人工中断),请步进到具体代码行后再校准")
	}
	if len(st.Source) == 0 {
		return 0, fmt.Errorf("当前停站没有源码上下文(仅紧凑停站),请步进后再校准")
	}
	// 参考行:IsCur 优先;否则取第一个非空文本行
	ref := SourceLine{}
	for _, sl := range st.Source {
		if sl.IsCur && strings.TrimSpace(sl.Text) != "" {
			ref = sl
			break
		}
	}
	if ref.Num == 0 {
		for _, sl := range st.Source {
			if strings.TrimSpace(sl.Text) != "" {
				ref = sl
				break
			}
		}
	}
	if ref.Num == 0 || strings.TrimSpace(ref.Text) == "" {
		return 0, fmt.Errorf("源码上下文没有可匹配的行文本")
	}
	sf, err := s.ResolveSource(st.File, s.Module)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.ReplaceAll(sf.Content, "\r\n", "\n"), "\n")
	// 验证集:源码块其余行相对 ref 的 DVM 行号差 → 文本,提高匹配唯一性
	verify := map[int]string{}
	for _, sl := range st.Source {
		if sl.Num == ref.Num {
			continue
		}
		if t := strings.TrimSpace(sl.Text); t != "" {
			verify[sl.Num-ref.Num] = t
		}
	}
	target := strings.TrimSpace(ref.Text)
	// 滑动搜索 offset ∈ [-5,5]:磁盘 idx = DVM行号 - offset - 1(0-based)
	for offset := -5; offset <= 5; offset++ {
		idx := ref.Num - offset - 1
		if idx < 0 || idx >= len(lines) || strings.TrimSpace(lines[idx]) != target {
			continue
		}
		ok := true
		for d, t := range verify {
			vi := idx + d
			if vi < 0 || vi >= len(lines) || strings.TrimSpace(lines[vi]) != t {
				ok = false
				break
			}
		}
		if ok {
			return offset, nil
		}
	}
	return 0, fmt.Errorf("未能在源码文件中匹配到停站行(文件与编译产物版本差异过大)")
}

// InfoLine 让 fgldb 解析位置(函数名/模块名/file:line)为 DVM 源文件名与行号,
// 是源码/符号定位的权威兜底(fgldeb 的 get_full_module_name 同款)
func (s *Session) InfoLine(loc string) (string, int, error) {
	r, err := s.exec("other", "info line "+loc, waitPrompt, 20*time.Second)
	if err != nil {
		return "", 0, err
	}
	if r.Err != "" {
		return "", 0, fmt.Errorf("%s", r.Err)
	}
	for _, ln := range r.Lines {
		if m := reInfoLine.FindStringSubmatch(ln); m != nil {
			n, _ := strconv.Atoi(m[1])
			return m[2], n, nil
		}
	}
	return "", 0, fmt.Errorf("无法解析 info line 输出: %s", strings.Join(r.Lines, " | "))
}

// Frame 选择栈帧(影响 print/locals 求值上下文);新停站自动回到栈顶
func (s *Session) Frame(n int) error {
	r, err := s.exec("other", fmt.Sprintf("frame %d", n), waitPrompt, 15*time.Second)
	if err != nil {
		return err
	}
	if r.Err != "" {
		return fmt.Errorf("%s", r.Err)
	}
	s.mu.Lock()
	s.curFrame = n
	s.mu.Unlock()
	return nil
}

// CurFrame 当前选中栈帧
func (s *Session) CurFrame() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.curFrame
}

// Autovars 最近一次停站的自动变量求值结果
func (s *Session) Autovars() []VarItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAutovars
}

// Step 步过/步入/步出/直到
func (s *Session) Step(cmd string) (*StopInfo, error) {
	if !s.Started() {
		// 入口停站:fgldb 在 run 之前不支持步进,透明等效为
		// 「tbreak main + run」= 启动并停在 MAIN 首条语句,符合常规调试器的直觉
		return s.stepFromEntry()
	}
	if cmd != "next" && cmd != "step" && cmd != "finish" && cmd != "until" && !strings.HasPrefix(cmd, "until ") {
		return nil, fmt.Errorf("不支持的步进命令: %s", cmd)
	}
	r, err := s.exec("step", cmd, waitPrompt, 60*time.Second)
	if err != nil {
		return nil, err
	}
	if r.Err != "" {
		return nil, fmt.Errorf("%s", r.Err)
	}
	if r.Stop != nil {
		return r.Stop, nil
	}
	if r.SawShell {
		// 步进过程中程序结束回 shell:本轮运行结束,会话保留(免重新登录)
		s.markShellReady()
		s.enterIdle("程序已退出,会话保留")
		return nil, nil
	}
	// 解析步进结果:优先带源码上下文的块(-> 行);
	// 源码不可用时 fgldb 只输出紧凑形式「89	 in lib_cl_ap.4gl」
	// 跨文件步入:fgldb 会输出停站头「func() at <file>:<line>」,
	// 文件名必须取自停站头,否则永远沿用上一次的文件(只跳行号不切文件)
	var src []SourceLine
	curLine := 0
	curFile := ""
	for _, ln := range r.Lines {
		if m := reStopHeader.FindStringSubmatch(ln); m != nil {
			curFile = m[2] // 步入公共函数时给出新文件
		}
		if m := reSource.FindStringSubmatch(ln); m != nil {
			num, _ := strconv.Atoi(m[2])
			src = append(src, SourceLine{Num: num, Text: m[3], IsCur: m[1] != ""})
			if m[1] != "" {
				curLine = num
			}
		}
	}
	var stop *StopInfo
	if curLine > 0 {
		file := curFile
		if file == "" {
			file = s.Cur().File // 无停站头(同文件步进)沿用当前文件
		}
		stop = &StopInfo{Reason: "step", File: file, Line: curLine, Source: src}
	} else {
		for _, ln := range r.Lines {
			if m := reCompactStep.FindStringSubmatch(ln); m != nil {
				n, _ := strconv.Atoi(m[1])
				stop = &StopInfo{Reason: "step", File: m[2], Line: n}
				break
			}
		}
	}
	if stop != nil {
		s.setStop(stop)
		s.autovarsSettle(stop) // 步进同步完成路径不走 onStop,这里补处理自动变量
		return stop, nil
	}
	return nil, nil
}

// WaitForStop 等待程序停站(断点命中/人工中断),供 AI 与 probe 在 run 之后使用
func (s *Session) WaitForStop(timeout time.Duration) (*StopInfo, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		switch s.State() {
		case StateStopped:
			st := s.Cur()
			return &st, nil
		case StateExit, StateIdle:
			return nil, fmt.Errorf("程序已退出")
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, fmt.Errorf("等待停站超时(%s)", timeout)
}

// stepFromEntry 入口停站的步进:临时断点在 main 首条语句 + run,
// 等效"从入口往下走一步";之后 started=true,后续步进为真实单步
// (注意:入口 pre-run 态下 continue 与 next 一样非法,必须用 run)
func (s *Session) stepFromEntry() (*StopInfo, error) {
	if _, err := s.exec("other", "tbreak main", waitPrompt, 15*time.Second); err != nil {
		return nil, fmt.Errorf("入口步进失败(tbreak main): %w", err)
	}
	s.markStarted()
	r, err := s.exec("run", "run", waitQuiet, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var stop *StopInfo
	syncDone := false
	if r.Stop != nil {
		// run 在静默判定前就命中了临时断点(同步完成路径,事件未发)
		stop = r.Stop
		syncDone = true
	} else {
		stop, err = s.WaitForStop(60 * time.Second)
		if err != nil {
			return nil, err
		}
	}
	s.emitEvent(Event{Type: "log", Text: "入口步进:已停在 MAIN 首条语句,后续步进为真实单步"})
	if syncDone {
		s.emitEvent(Event{Type: "stopped", Stop: stop})
	}
	s.startWatchdog()
	return stop, nil
}

// Raw 透传任意调试命令(供 AI 高级用法),返回原始行
func (s *Session) Raw(cmd string, timeout time.Duration) ([]string, error) {
	r, err := s.exec("other", cmd, waitPrompt, timeout)
	if err != nil {
		return nil, err
	}
	return r.Lines, nil
}

// ---------- 输出泵 ----------

func (s *Session) pump() {
	defer close(s.done)
	pr := &host.LineParser{}
	buf := make([]byte, 8192)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			for _, ln := range pr.Feed(buf[:n]) {
				s.onLine(ln)
			}
			// 提示符后面没有换行:半行若已是提示符形态,立即出行
			// (PS1 与 (fgldb) 都是"无换行"输出,否则永远等不到)
			if partial := pr.PartialStr(); partial != "" {
				if host.IsBarePrompt(partial) || host.ReShellPrompt.MatchString(partial) {
					s.onLine(pr.FlushPartial())
				}
			}
		}
		if err != nil {
			pr.Rest()
			s.handleEOF()
			return
		}
	}
}

func (s *Session) handleEOF() {
	s.mu.Lock()
	st := s.state
	already := st == StateExit
	s.mu.Unlock()
	if !already {
		s.emitEvent(Event{Type: "dead", Text: "调试后端连接断开(终端流结束),会话已终止,可重新启动"})
		s.forceExit()
	}
}

// pushLine 把行推给启动序列等待者(非阻塞,防泵卡死)
func (s *Session) pushLine(ln string) {
	select {
	case s.lines <- ln:
	default:
	}
}

func (s *Session) onLine(ln string) {
	if ln == "" {
		return
	}
	if rawDebug {
		log.Println("RAW " + strconv.Quote(ln))
	}
	s.pushLine(ln)
	s.emitEvent(Event{Type: "output", Text: ln})

	s.mu.Lock()
	pending := s.pending
	collect := s.collect
	state := s.state
	quitReq := s.quitReq
	s.mu.Unlock()

	// ---- 停站块开启 ----
	if collect == nil {
		if rawDebug {
			log.Println("DECIDE state=" + string(state) + " pending=" + strconv.FormatBool(pending != nil) + " line=" + strconv.Quote(ln))
		}
		// SIGINT 的 ^C 回显粘在后续输出上,且该 DVM 中断时不输出 INTERRUPT 字样,
		// 只输出源码块——^C 前缀本身就是中断标记;剥离后本行仍需按内容收集
		sigint := false
		if state == StateRunning && strings.HasPrefix(ln, "^C") {
			ln = strings.TrimPrefix(ln, "^C")
			collect = &stopCollect{reason: "interrupt"}
			sigint = true
		}
		if !sigint {
			if m := reBreakHit.FindStringSubmatch(ln); m != nil {
				num, _ := strconv.Atoi(m[1])
				line, _ := strconv.Atoi(m[4])
				collect = &stopCollect{reason: "breakpoint", bpnum: num, fn: m[2], file: m[3], line: line}
			} else if reSource.MatchString(ln) {
				// 源码块行(如 `-> 1532 DEFER INTERRUPT`):行内容含关键字,
				// 不是人工中断标记,不能开启中断收集
			} else if reInterrupt.MatchString(ln) {
				collect = &stopCollect{reason: "interrupt"}
			} else if state == StateRunning && !reFrame.MatchString(ln) {
				if m := reStopHeader.FindStringSubmatch(ln); m != nil {
					line, _ := strconv.Atoi(m[3])
					collect = &stopCollect{reason: "step", fn: m[1], file: m[2], line: line}
				}
			}
		}
		if collect != nil {
			s.mu.Lock()
			s.collect = collect
			s.mu.Unlock()
		}
	}

	// ---- 停站块收口:裸提示符 ----
	if collect != nil {
		if host.IsBarePrompt(ln) {
			// 中断块没有位置头:从箭头行补行号(文件名协议未给,由 where/前端兜底)
			if collect.reason == "interrupt" && collect.file == "" && collect.line == 0 {
				for _, sl := range collect.source {
					if sl.IsCur {
						collect.line = sl.Num
						break
					}
				}
			}
			stop := &StopInfo{
				Reason: collect.reason, BPNum: collect.bpnum,
				Func: collect.fn, File: collect.file, Line: collect.line,
				Frames: collect.frames, Source: collect.source,
			}
			s.mu.Lock()
			s.collect = nil
			s.cur = *stop
			pending := s.pending
			s.mu.Unlock()
			s.onStop(stop, pending, quitReq)
			return
		}
		// 内容收集
		if m := reFrame.FindStringSubmatch(ln); m != nil {
			idx, _ := strconv.Atoi(m[1])
			line, _ := strconv.Atoi(m[4])
			collect.frames = append(collect.frames, Frame{Idx: idx, Func: m[2], File: m[3], Line: line})
		} else if m := reSource.FindStringSubmatch(ln); m != nil {
			num, _ := strconv.Atoi(m[2])
			collect.source = append(collect.source, SourceLine{Num: num, Text: m[3], IsCur: m[1] != ""})
		}
		return
	}

	// ---- pending 响应处理(非停站块) ----
	if pending != nil {
		pending.addLine(ln)

		// 程序退出(作业窗口被关闭或正常结束):立即结束本轮命令,回空闲保留宿主,
		// 避免 waitMarker/waitPrompt 等不到完成信号而超时,也避免被裸提示符误判成停站
		if reProgramExited.MatchString(ln) {
			s.completePending(&execResult{Cmd: pending.cmd, Lines: pending.lines, Err: "program exited"})
			s.enterIdle("程序已退出(作业窗口被关闭或正常结束),会话保留")
			return
		}

		if e := matchFdbErr(ln); e != "" && pending.errText == "" {
			pending.errText = e
		}

		switch pending.kind {
		case "where":
			if m := reFrame.FindStringSubmatch(ln); m != nil {
				idx, _ := strconv.Atoi(m[1])
				line, _ := strconv.Atoi(m[4])
				pending.frames = append(pending.frames, Frame{Idx: idx, Func: m[2], File: m[3], Line: line})
			}
		case "print":
			if m := rePrintVal.FindStringSubmatch(ln); m != nil {
				pending.valueOn = true
				pending.value.Reset()
				pending.writeValue(m[2])
			} else if pending.valueOn && !host.IsBarePrompt(ln) {
				pending.writeValue("\n" + ln)
			}
		case "break":
			if m := reBPSet.FindStringSubmatch(ln); m != nil {
				num, _ := strconv.Atoi(m[1])
				line, _ := strconv.Atoi(m[3])
				s.mu.Lock()
				s.bps[num] = &Breakpoint{Num: num, File: m[2], Line: line, Enabled: true}
				s.mu.Unlock()
			}
		case "breakpoints":
			if m := reBPInfo.FindStringSubmatch(ln); m != nil {
				num, _ := strconv.Atoi(m[1])
				line, _ := strconv.Atoi(m[5])
				pending.bps = append(pending.bps, Breakpoint{Num: num, Func: m[3], File: m[4], Line: line, Enabled: m[2] == "y"})
			}
		}

		// 完成条件
		if host.ReShellPrompt.MatchString(ln) {
			// fgldb 已退出回到 shell(quit 后)
			s.completePending(&execResult{Cmd: pending.cmd, SawShell: true, Err: pending.errText})
			return
		}
		if host.IsBarePrompt(ln) && pending.mode == waitPrompt {
			s.completePending(&execResult{
				Cmd: pending.cmd, Lines: pending.lines,
				Frames: pending.frames, Value: pending.value.String(), BPs: pending.bps,
				Err: pending.errText,
			})
			return
		}
		if reContinuing.MatchString(ln) && pending.mode == waitMarker {
			r := &execResult{Cmd: pending.cmd, Lines: pending.lines, Continuing: true}
			s.completePending(r)
			s.setState(StateRunning)
			return
		}
		// waitQuiet:出现任意实质输出即认为已接受(异步停站由泵兜底)
		if pending.mode == waitQuiet && len(pending.lines) > 0 && !host.IsBarePrompt(ln) && !strings.HasPrefix(ln, "(fgldb)") {
			s.completePending(&execResult{Cmd: pending.cmd, Lines: pending.lines})
			s.setState(StateRunning)
			return
		}
		if host.IsBarePrompt(ln) && pending.mode == waitQuiet {
			// run 失败/立即停站:回到 stopped
			s.completePending(&execResult{Cmd: pending.cmd, Lines: pending.lines})
			s.setState(StateStopped)
			s.startWatchdog()
			return
		}
		return
	}

	// ---- 无 pending 时的异步状态迁移 ----
	// idle 收尾:本轮已结束,等待宿主回 shell。残留的 (fgldb)(自然退出后
	// fglrun 未随程序退出)不在 idle 期补 quit——统一交给下次启动的
	// ensureShellForRun 清理,避免与启动流程重复发 quit
	if state == StateIdle {
		if host.ReShellPrompt.MatchString(ln) {
			s.markShellReady()
		}
		return // idle 期其它输出(退出回显等)直接忽略
	}
	if reProgramExited.MatchString(ln) {
		// 程序退出(作业窗口被关闭或正常结束):本轮运行结束,回空闲保留宿主;
		// 不能当停站处理,否则后续裸提示符会被下面的"保守置 stopped"误判
		s.enterIdle("程序已退出(作业窗口被关闭或正常结束),会话保留")
		return
	}
	if reContinuing.MatchString(ln) && state == StateStopped {
		s.setState(StateRunning)
		return
	}
	if host.IsBarePrompt(ln) && state == StateRunning {
		// 程序停下但没有可识别的停站头(罕见)——保守置为 stopped
		s.setState(StateStopped)
		s.startWatchdog()
		return
	}
	if host.ReShellPrompt.MatchString(ln) && state == StateRunning {
		// 程序运行中看到 shell 提示符 = 程序已退出回到 shell
		s.markShellReady()
		s.enterIdle("程序已退出,会话保留")
	}
}

// onStop 停站收口:同步(命令响应)或异步(断点/中断命中)
func (s *Session) onStop(stop *StopInfo, pending *pendingCmd, quitReq bool) {
	if pending != nil {
		// 停站发生在命令执行中:完成该命令(step 类),状态保持 stopped
		s.completePending(&execResult{
			Cmd: pending.cmd, Lines: pending.lines, Stop: stop,
			Frames: pending.frames, Value: pending.value.String(), BPs: pending.bps,
			Err: pending.errText,
		})
		s.startWatchdog()
		if !quitReq {
			s.autovarsSettle(stop)
		}
		return
	}
	// 异步命中:断点/SIGINT
	if quitReq {
		return // 正在退出,不用理会
	}
	s.setState(StateStopped)
	s.emitEvent(Event{Type: "stopped", Stop: stop})
	s.startWatchdog()
	s.autovarsSettle(stop)
}

// ---------- 状态与看门狗 ----------

func (s *Session) setState(st State) {
	s.mu.Lock()
	old := s.state
	s.state = st
	if st == StateStopped {
		s.startAt = time.Now()
	}
	s.mu.Unlock()
	if st == StateRunning {
		s.stopWatchdog() // 离开停站态,取消看门狗计时
	}
	if old != st {
		s.emitEvent(Event{Type: "state", State: string(st)})
	}
}

func (s *Session) setStop(stop *StopInfo) {
	s.mu.Lock()
	s.cur = *stop
	s.curFrame = 0 // 新停站回到栈顶帧
	s.mu.Unlock()
}

func (s *Session) startWatchdog() {
	if s.cfg.WatchdogSeconds <= 0 {
		return
	}
	if s.stopTimer != nil {
		s.stopTimer.Stop()
	}
	cur := s.Cur()
	if cur.Reason == "entry" {
		return // 入口停留不持事务锁,不自动 continue
	}
	s.stopTimer = time.AfterFunc(time.Duration(s.cfg.WatchdogSeconds)*time.Second, func() {
		if s.State() != StateStopped {
			return
		}
		s.emitEvent(Event{Type: "watchdog",
			Text: fmt.Sprintf("停站停留超过 %d 秒,看门狗自动继续运行(防生产库行锁)", s.cfg.WatchdogSeconds)})
		go s.Continue()
	})
}

// StopTimer 停掉看门狗计时(继续运行时)
func (s *Session) stopWatchdog() {
	if s.stopTimer != nil {
		s.stopTimer.Stop()
	}
}

// ---------- 等待辅助(启动序列用) ----------

func (s *Session) waitRegexp(re *regexp.Regexp, timeout time.Duration, what string) error {
	deadline := time.After(timeout)
	have := false
	for !have {
		select {
		case ln := <-s.lines:
			if re.MatchString(ln) {
				have = true
			}
		case <-deadline:
			return fmt.Errorf("等待 %s 超时", what)
		}
	}
	return nil
}

func (s *Session) waitBarePrompt(timeout time.Duration, what string) error {
	deadline := time.After(timeout)
	for {
		select {
		case ln := <-s.lines:
			if host.IsBarePrompt(ln) {
				return nil
			}
		case <-deadline:
			return fmt.Errorf("等待 %s 超时", what)
		}
	}
}

// ---------- 源码读取(SFTP) ----------

// sftpClient 懒加载 SFTP 通道
func (s *Session) sftpClient() (*sftp.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sftpClientCache == nil {
		c, err := s.conn.SFTP()
		if err != nil {
			return nil, fmt.Errorf("打开 SFTP 失败: %w", err)
		}
		s.sftpClientCache = c
	}
	return s.sftpClientCache, nil
}

// ReadFile 读取服务器文件
func (s *Session) ReadFile(path string) ([]byte, time.Time, error) {
	cl, err := s.sftpClient()
	if err != nil {
		return nil, time.Time{}, err
	}
	f, err := cl.Open(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer f.Close()
	var mtime time.Time
	if fi, err := f.Stat(); err == nil {
		mtime = fi.ModTime()
	}
	data, err := io.ReadAll(f)
	return data, mtime, err
}

// SourceFile 解析后的源码文件
type SourceFile struct {
	DVMFile string    `json:"dvmFile"` // DVM 报告的模块源名,如 asf_bsft001_wf.4gl
	Path    string    `json:"path"`    // 实际读取的服务器路径
	Content string    `json:"content"`
	ModTime time.Time `json:"modTime"`
}

type srcCacheEntry struct {
	modTime time.Time
	file    SourceFile
}

// sourceCandidatePaths 生成源码查找候选路径(自由函数,会话与预取共用)
// 模块自有目录(4gl/42m)优先,随后是公共库目录(com 的 lib/sub/qry 及 c 前缀变体)
func sourceCandidatePaths(roots []string, module, dvmFile string) []string {
	name := strings.TrimSuffix(dvmFile, ".4gl")
	master := name
	if module != "" && strings.HasPrefix(name, module+"_") {
		master = strings.TrimPrefix(name, module+"_")
	}
	var dirs []string
	for _, root := range roots {
		if module != "" {
			// 客制目录(首字母 a→c,ain→cin)优先:转客制源码在 cin/4gl 等
			if strings.HasPrefix(module, "a") && reProgName.MatchString(module) {
				dirs = append(dirs, root+"/c"+module[1:]+"/4gl", root+"/c"+module[1:]+"/42m")
			}
			dirs = append(dirs, root+"/"+module+"/4gl", root+"/"+module+"/42m")
		}
		dirs = append(dirs,
			root+"/4gl", root+"/42m",
			root+"/lib/42m", root+"/sub/42m", root+"/qry/42m",
			root+"/clib/42m", root+"/csub/42m", root+"/cqry/42m",
			root+"/lib/4gl",
		)
	}
	var out []string
	seen := map[string]bool{}
	for _, d := range dirs {
		for _, n := range []string{master + ".4gl", name + ".4gl"} {
			p := d + "/" + n
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// sourceCandidates 生成源码查找候选路径
func (s *Session) sourceCandidates(dvmFile, module string) []string {
	return sourceCandidatePaths(s.cfg.ModuleRootsActual(), module, dvmFile)
}

// ResolveSource 把 DVM 报告的源文件名解析为真实路径并读取(带 mtime 缓存,避免每次停站全量拉取)
func (s *Session) ResolveSource(dvmFile, module string) (*SourceFile, error) {
	cl, err := s.sftpClient()
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, p := range s.sourceCandidates(dvmFile, module) {
		fi, err := cl.Stat(p)
		if err != nil {
			lastErr = err
			continue
		}
		mt := fi.ModTime()
		s.mu.Lock()
		e, ok := s.srcCache[p]
		s.mu.Unlock()
		if ok && e.modTime.Equal(mt) {
			sf := e.file
			return &sf, nil
		}
		data, rmt, err := s.ReadFile(p)
		if err != nil {
			lastErr = err
			continue
		}
		sf := SourceFile{DVMFile: dvmFile, Path: p, Content: string(data), ModTime: rmt}
		s.mu.Lock()
		if len(s.srcCache) > 24 {
			s.srcCache = map[string]srcCacheEntry{}
		}
		s.srcCache[p] = srcCacheEntry{modTime: mt, file: sf}
		s.mu.Unlock()
		return &sf, nil
	}
	return nil, fmt.Errorf("源码未找到(%s): %w", dvmFile, lastErr)
}

// ReadPath 读取任意路径(限制在登录区源码目录内,防止越权读文件)
func (s *Session) ReadPath(path string) (*SourceFile, error) {
	ok := false
	for _, root := range s.cfg.ModuleRootsActual() {
		if strings.HasPrefix(path, root+"/") {
			ok = true
			break
		}
	}
	if !ok {
		return nil, fmt.Errorf("路径不在登录区源码目录内: %s", path)
	}
	data, mt, err := s.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &SourceFile{Path: path, Content: string(data), ModTime: mt}, nil
}

// ---------- 快照 ----------

// State 当前状态
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 入口停站后的断点恢复期间,对外仍报 loading:
	// 前端见到 stopped 即停轮询,提前宣告会让首帧快照缺断点(要步进一次才可见)
	if s.state == StateStopped && s.restoring {
		return StateLoading
	}
	return s.state
}

// Cur 最近停站现场
func (s *Session) Cur() StopInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// Breakpoints 断点列表
func (s *Session) Breakpoints() []Breakpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Breakpoint, 0, len(s.bps))
	for _, b := range s.bps {
		out = append(out, *b)
	}
	// map 遍历顺序随机:按 文件+行号 稳定排序,保证快照/列表刷新(如步进后)顺序不变
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// HoldingSeconds 已停留秒数(仅 stopped 态有意义)
func (s *Session) HoldingSeconds() float64 {
	s.mu.Lock()
	st := s.state
	start := s.startAt
	s.mu.Unlock()
	if st != StateStopped {
		return 0
	}
	return time.Since(start).Seconds()
}

func (s *Session) emitEvent(ev Event) {
	if s.emit == nil {
		return
	}
	ev.SessionID = s.ID
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	s.emit(ev)
}

// ---------- 自动变量(fgldeb Auto 面板等价物) ----------

// extractVarNames 从停站源码窗提取候选变量名:当前行优先,其余按距离;
// 剥注释与字符串字面量,排除 4gl 关键字与函数调用,去重,上限 max
func extractVarNames(source []SourceLine, curLine, max int) []string {
	if curLine <= 0 || len(source) == 0 {
		return nil
	}
	win := make([]SourceLine, len(source))
	copy(win, source)
	sort.SliceStable(win, func(i, j int) bool {
		di, dj := absInt(win[i].Num-curLine), absInt(win[j].Num-curLine)
		if di != dj {
			return di < dj
		}
		return win[i].Num < win[j].Num
	})
	seen := map[string]bool{}
	var out []string
	for _, sl := range win {
		for _, name := range lineVarNames(sl.Text) {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
				if len(out) >= max {
					return out
				}
			}
		}
	}
	return out
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// reIdent 标识符(含记录链 a.b.c)
var reIdent = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*`)

// lineVarNames 从一行 4gl 源码提取候选变量名
func lineVarNames(line string) []string {
	if i := strings.Index(line, "--"); i >= 0 {
		line = line[:i]
	}
	if i := strings.Index(line, "#"); i >= 0 {
		line = line[:i]
	}
	line = stripQuoted(line)
	var out []string
	seen := map[string]bool{}
	for _, loc := range reIdent.FindAllStringIndex(line, -1) {
		name := line[loc[0]:loc[1]]
		// 函数调用排除:紧跟 "("
		if strings.HasPrefix(strings.TrimLeft(line[loc[1]:], " \t"), "(") {
			continue
		}
		skip := false
		for _, seg := range strings.Split(name, ".") {
			if glKeywords[strings.ToLower(seg)] {
				skip = true
				break
			}
		}
		if skip || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// stripQuoted 把 '...' 与 "..." 字面量替换为空格(处理反斜杠转义)
func stripQuoted(s string) string {
	var b strings.Builder
	in := byte(0)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if in != 0 {
			if ch == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(' ')
				continue
			}
			if ch == in {
				in = 0
			}
			b.WriteByte(' ')
			continue
		}
		if ch == '\'' || ch == '"' {
			in = ch
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

// glKeywords 4gl 关键字表(变量名排除用,取自 BDL 语言关键字)
var glKeywords = map[string]bool{}

func init() {
	kws := []string{
		"main", "function", "return", "if", "then", "else", "elif", "for", "to", "step",
		"while", "case", "when", "otherwise", "define", "record", "array", "dynamic", "like",
		"type", "constant", "let", "call", "display", "input", "construct", "by", "name",
		"on", "from", "menu", "command", "continue", "exit", "next", "field", "before",
		"after", "row", "key", "options", "defer", "interrupt", "whenever", "error",
		"warning", "open", "window", "form", "close", "current", "dialog", "attributes",
		"unbuffered", "without", "defaults", "accept", "cancel", "insert", "delete",
		"update", "select", "foreach", "execute", "immediate", "prepare", "declare",
		"fetch", "free", "rollback", "work", "commit", "run", "sleep", "import", "fgl",
		"public", "private", "returns", "returning", "null", "true", "false", "not",
		"and", "or", "is", "in", "goto", "label", "end", "with", "use", "every", "clear",
		"show", "prompt", "arrange", "grid", "scroll", "touch", "touching", "timer",
		"idle", "action", "interact", "connect", "disconnect", "set", "transaction",
		"begin", "count", "terminate", "report", "output", "order", "group", "having",
		"where", "values", "into", "database", "schema", "table", "temp", "validate",
	}
	for _, w := range kws {
		glKeywords[w] = true
	}
}

// AutovarsOn 停站后自动求值自动变量的开关状态(默认关,由前端面板开关打开)
func (s *Session) AutovarsOn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.autovarsOn
}

// SetAutovarsOn 开关停站后自动求值自动变量。默认关:每次停站自动调度对窗口变量
// 逐个 print 求值会占住命令队列、拖慢步进(见 autovarsSettle)。
// 开启且当前已停站时立即补一次求值,结果照常以 autovars 事件推送。
func (s *Session) SetAutovarsOn(on bool) {
	s.mu.Lock()
	s.autovarsOn = on
	cur := s.cur
	st := s.state
	s.mu.Unlock()
	if on && st == StateStopped && cur.Line > 0 {
		go s.evaluateAutovars(&cur)
	}
}

// autovarsSettle 停站收口处理自动变量:开关开 → 后台求值;关 → 清掉旧值并广播空,
// 避免面板残留上一停站的结果误导(默认关时每次停站不产生求值命令)。
func (s *Session) autovarsSettle(stop *StopInfo) {
	if s.AutovarsOn() {
		if stop != nil {
			go s.evaluateAutovars(stop)
		}
		return
	}
	s.mu.Lock()
	had := len(s.lastAutovars) > 0
	s.lastAutovars = nil
	s.mu.Unlock()
	if had {
		s.emitEvent(Event{Type: "autovars", Vars: nil})
	}
}

// evaluateAutovars 停站后自动提取当前源码窗中的变量并求值。
// 尽力而为:程序已继续则静默放弃,绝不干扰主流程;结果广播 autovars 事件。
func (s *Session) evaluateAutovars(stop *StopInfo) {
	defer func() { _ = recover() }() // 后台任务,任何异常不影响会话
	if stop == nil || stop.Line <= 0 || !s.Started() {
		return
	}
	names := extractVarNames(stop.Source, stop.Line, 8)
	out := make([]VarItem, 0, len(names))
	for _, name := range names {
		if len(out) >= 8 {
			break
		}
		v, err := s.Print(name)
		if err != nil {
			if errors.Is(err, ErrNotStopped) {
				return // 程序已继续
			}
			continue // No symbol 等:不是有效变量名,丢弃
		}
		out = append(out, VarItem{Expr: name, Value: v})
	}
	s.mu.Lock()
	s.lastAutovars = out
	s.mu.Unlock()
	s.emitEvent(Event{Type: "autovars", Vars: out})
}

// ---------- 断点持久化(fgldeb 状态文件思路) ----------

// persistBPs 把当前断点(fgldb 真相)连同行文本落盘;尽力而为,失败仅记日志
func (s *Session) persistBPs() {
	if !s.cfg.BPsPersisted() {
		return
	}
	s.mu.Lock()
	bps := make([]Breakpoint, 0, len(s.bps))
	for _, b := range s.bps {
		bps = append(bps, *b)
	}
	s.mu.Unlock()
	stored := make([]StoredBP, 0, len(bps))
	srcCache := map[string][]string{}
	for _, b := range bps {
		lines, ok := srcCache[b.File]
		if !ok {
			if sf, err := s.ResolveSource(b.File, s.Module); err == nil {
				lines = strings.Split(sf.Content, "\n")
			}
			srcCache[b.File] = lines
		}
		lt := ""
		if lines != nil && b.Line >= 1 && b.Line <= len(lines) {
			lt = strings.TrimSpace(lines[b.Line-1])
		}
		stored = append(stored, StoredBP{File: b.File, Line: b.Line, Func: b.Func, Enabled: b.Enabled, LineText: lt})
	}
	if err := saveBPs(s.cfg.DataDir, s.Module, s.Prog, stored); err != nil {
		s.emitEvent(Event{Type: "log", Text: "断点保存失败: " + err.Error()})
	}
}

// restoreBreakpoints 启动后从持久化文件恢复断点:
// 按保存的行文本在源码中重新定位(±15 行),源码变更不恢复错位置,定位不到跳过
func (s *Session) restoreBreakpoints() {
	if !s.cfg.BPsPersisted() {
		return
	}
	st, err := loadBPs(s.cfg.DataDir, s.Module, s.Prog)
	if err != nil || len(st.Breakpoints) == 0 {
		return
	}
	restored, skipped := 0, 0
	srcCache := map[string][]string{}
	for _, sb := range st.Breakpoints {
		loc := fmt.Sprintf("%s:%d", sb.File, sb.Line)
		if sb.LineText != "" {
			lines, ok := srcCache[sb.File]
			if !ok {
				if sf, err := s.ResolveSource(sb.File, s.Module); err == nil {
					lines = strings.Split(sf.Content, "\n")
				}
				srcCache[sb.File] = lines
			}
			if lines != nil {
				nl, found := relocateLine(lines, sb.Line, sb.LineText)
				if !found {
					skipped++
					s.emitEvent(Event{Type: "log", Text: fmt.Sprintf("断点恢复跳过:%s:%d 与源码不再匹配", sb.File, sb.Line)})
					continue
				}
				if nl != sb.Line {
					s.emitEvent(Event{Type: "log", Text: fmt.Sprintf("断点重定位:%s %d → %d(源码已变更)", sb.File, sb.Line, nl)})
				}
				loc = fmt.Sprintf("%s:%d", sb.File, nl)
			}
		}
		bp, err := s.Break(loc)
		if err != nil {
			skipped++
			s.emitEvent(Event{Type: "log", Text: fmt.Sprintf("断点恢复失败:%s: %v", loc, err)})
			continue
		}
		restored++
		if !sb.Enabled && bp.Note == "" {
			_ = s.SetBPEnabled(bp.Num, false)
		}
	}
	if restored > 0 || skipped > 0 {
		s.emitEvent(Event{Type: "log", Text: fmt.Sprintf("断点恢复完成:%d 成功,%d 跳过(共 %d)", restored, skipped, len(st.Breakpoints))})
	}
	s.persistBPs() // 回写(行号可能已重定位)
}

// relocateLine 按行文本重新定位行号:先精确匹配,再 ±15 行窗口内找相同文本
func relocateLine(lines []string, line int, text string) (int, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return line, true // 无行文本存档(旧文件):按原行号直下,由 fgldb 自行调整
	}
	if line >= 1 && line <= len(lines) && strings.TrimSpace(lines[line-1]) == text {
		return line, true
	}
	for d := 1; d <= 15; d++ {
		for _, idx := range []int{line - d, line + d} {
			if idx >= 1 && idx <= len(lines) && strings.TrimSpace(lines[idx-1]) == text {
				return idx, true
			}
		}
	}
	return 0, false
}
