package debug

import (
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"tdebug/host"
	"time"

	"github.com/pkg/sftp"
)

// Manager 调试会话管理器:持有会话、向订阅者(WS/MCP)广播事件、保留最近事件供查询
type Manager struct {
	cfg *Config

	mu       sync.Mutex
	sessions map[string]*Session

	subMu sync.Mutex
	subs  map[chan Event]string

	// 最近事件环形缓冲(供 AI/日志 tail 查询;只存结构化事件,output/autovars 不入)
	evMu sync.Mutex
	evs  []Event

	// seq 事件单调序号。用原子而非在 evMu 里自增:output 事件也要有序号
	// (前端统一按序号去重),而 output 不入缓冲、根本不进 evMu。
	// 序号是进程级的、重启归零,所以配一个 Epoch 让前端知道该重置游标。
	seq   atomic.Uint64
	Epoch string

	// T100 动态环境缓存(登录区域 → 路径),TTL 5 分钟;探针失败不缓存
	envMu    sync.Mutex
	envCache map[string]cachedEnv

	// 会话外源码读取共用的连接(见 srcmirror.go):
	// 每次 tdebug source/grep 重新握手太贵,按 SSH 目标缓存一条长连接。
	srcMu sync.Mutex
	src   *srcConn
}

// maxBufferedEvents 环形缓冲上限。
const maxBufferedEvents = 4000

type cachedEnv struct {
	env *host.RuntimeEnv
	at  time.Time
}

func NewManager(cfg *Config) *Manager {
	return &Manager{
		cfg:      cfg,
		sessions: map[string]*Session{},
		subs:     map[chan Event]string{},
		envCache: map[string]cachedEnv{},
		Epoch:    strconv.FormatInt(time.Now().UnixNano(), 36),
	}
}

// getRuntimeEnv 取 T100 动态环境:缓存命中直接返回;未命中(或过期)探针一次并缓存。
// 失败返回错误(不缓存),调用方须报错,无静态配置可回退。key 按 服务器+账号+区域 区分
// (用连接的真实 host,覆盖/多环境时不受 active 环境影响),换环境互不污染。
func (m *Manager) getRuntimeEnv(conn *host.SSHConn, zone string) (*host.RuntimeEnv, error) {
	cc := conn.Cfg()
	key := cc.Host + "|" + cc.User + "|" + zone
	m.envMu.Lock()
	if c, ok := m.envCache[key]; ok && time.Since(c.at) < 5*time.Minute && c.env.Valid() {
		m.envMu.Unlock()
		return c.env, nil
	}
	m.envMu.Unlock()
	env, err := host.ProbeTEnv(conn, zone)
	if err != nil {
		return nil, err
	}
	m.envMu.Lock()
	m.envCache[key] = cachedEnv{env: env, at: time.Now()}
	m.envMu.Unlock()
	return env, nil
}

// ensureRuntimeEnv 确保 cfg.Runtime 有动态路径(探针+缓存);失败返回错误,无静态兜底。
func (m *Manager) ensureRuntimeEnv(conn *host.SSHConn) error {
	if m.cfg.Runtime != nil && m.cfg.Runtime.Valid() {
		return nil
	}
	zone := m.cfg.Zone
	if zone == "" {
		zone = "36"
	}
	env, err := m.getRuntimeEnv(conn, zone)
	if err != nil {
		return fmt.Errorf("无法获取环境 %s(zone %s)的 T100 路径: %w", m.cfg.EnvName(), zone, err)
	}
	m.cfg.Runtime = env
	return nil
}

// Subscribe 订阅事件流(WS 用),返回取消函数
func (m *Manager) Subscribe(tag string) (<-chan Event, func()) {
	ch := make(chan Event, 4096)
	m.subMu.Lock()
	m.subs[ch] = tag
	m.subMu.Unlock()
	return ch, func() {
		m.subMu.Lock()
		delete(m.subs, ch)
		m.subMu.Unlock()
	}
}

// SubscribeWithReplay 订阅事件流,同时取回最近 n 条历史(供 WS 建连时开场补发)。
//
// 订阅与取快照放进**同一个 evMu→subMu 临界区**(锁序与 emit 一致):
// 这样保证**不遗漏** —— emit 的 append 先于 broadcast,窗口内产生的事件
// 要么已经进了快照、要么会进 channel。但**仍可能重复**(emit 的 append 与
// broadcast 之间不持锁),所以调用方必须用 Seq 去重。
func (m *Manager) SubscribeWithReplay(tag string, n int) (<-chan Event, []Event, func()) {
	ch := make(chan Event, 4096)
	m.evMu.Lock()
	m.subMu.Lock()
	m.subs[ch] = tag
	m.subMu.Unlock()
	var replay []Event
	if n > 0 && len(m.evs) > 0 {
		start := len(m.evs) - n
		if start < 0 {
			start = 0
		}
		replay = make([]Event, len(m.evs)-start)
		copy(replay, m.evs[start:])
	}
	m.evMu.Unlock()
	cancel := func() {
		m.subMu.Lock()
		delete(m.subs, ch)
		m.subMu.Unlock()
	}
	return ch, replay, cancel
}

// emit 会话事件回调:入环形缓冲并广播给所有订阅者(非阻塞,满则丢弃)
func (m *Manager) emit(ev Event) {
	ev.Seq = m.seq.Add(1) // 第一件事:序号必须先于任何入缓冲/广播动作
	// 只保留对"发生了什么"有信息量的结构化事件(输出流/自动变量列表不入缓冲,
	// 会占用大量内存且 AI 用 tail 查询时噪声过大)
	switch ev.Type {
	case "state", "stopped", "log", "dead", "watchdog", "ai_action":
		m.evMu.Lock()
		m.evs = append(m.evs, ev)
		if len(m.evs) > maxBufferedEvents {
			m.evs = m.evs[len(m.evs)-maxBufferedEvents:]
		}
		m.evMu.Unlock()
	}
	m.subMu.Lock()
	defer m.subMu.Unlock()
	for ch := range m.subs {
		select {
		case ch <- ev:
		default: // 订阅者消费太慢,丢弃(前端靠 state/stopped 事件对齐,丢原始行无碍)
		}
	}
}

// Events 返回最近 n 条事件(按时间升序;n<=0 返回全部缓冲)。
func (m *Manager) Events(n int) []Event {
	m.evMu.Lock()
	defer m.evMu.Unlock()
	if n <= 0 || n >= len(m.evs) {
		out := make([]Event, len(m.evs))
		copy(out, m.evs)
		return out
	}
	out := make([]Event, n)
	copy(out, m.evs[len(m.evs)-n:])
	return out
}

// Launch 创建并启动一个调试会话(同一时间仅允许一个会话,保证生产安全)。
// 启动前清扫已结束的残留会话(程序退出/后端死亡),重启无需先 DELETE。
func (m *Manager) Launch(module, prog string) (*Session, error) {
	return m.launchWith(m.cfg, module, prog)
}

// LaunchWith 按指定配置启动(多 SSH/多区域:调用方克隆好 cfg 再传入)
func (m *Manager) LaunchWith(cfg *Config, module, prog string) (*Session, error) {
	return m.launchWith(cfg, module, prog)
}

func (m *Manager) launchWith(cfg *Config, module, prog string) (*Session, error) {
	// 对齐 T100 gendbg:启动前连库把作业编号解析成实体程序+模块+启动引用(gzzz_t JOIN gzza_t),
	// 源码与 42r 都跟实体程序走;未配置 db/查询失败/未命中 → 会话内按名称文件搜索兜底
	runProg, launchRef, extra := "", "", ""
	if mod2, prog2, ref2, extra2, rerr := m.resolveJobWith(cfg, module, prog); rerr != nil {
		return nil, rerr
	} else if prog2 != "" {
		runProg = prog2
		launchRef, extra = ref2, extra2
		if module == "" && mod2 != "" {
			module = mod2
		}
	}
	sess, err := m.prepareSession(cfg, module, prog, runProg, launchRef, extra, "")
	if err != nil {
		return nil, err
	}
	// 新的一轮运行:清空上一轮攒下的源码副本(见 srcmirror.go)。
	// 放在 prepareSession 成功之后 —— 它可能因"已有活跃运行"而拒绝,
	// 那种失败不该顺手毁掉正在跑的那一轮的副本。
	// 两件事必须成对:清了目录还要清会话的"已镜像"记录,否则复用的宿主会话
	// 会以为文件早就镜像过,新一轮再也落不下副本。
	clearMirror(cfg.DataDir)
	sess.resetMirrored()
	if runProg != "" && runProg != prog {
		sess.emitEvent(Event{Type: "log", Text: fmt.Sprintf("作业编号 %s → 实体程序 %s(gzzz_t)", prog, runProg)})
	}
	return sess, nil
}

// prepareSession 启动一轮运行前的会话准备(单一常驻会话):
// - 已有空闲(idle)且目标相同的会话 → 复用宿主,仅刷新本轮运行参数(免重新登录);
// - 已有会话但目标不同(默认环境切换/按名启动其它服务器)→ 断开旧的再新建;
// - 已有活跃运行且目标相同 → 报错(请先结束当前调试);
// - 无会话 → 新建。
// 返回已登记的会话,由调用方驱动 Launch 登录/启动。
func (m *Manager) prepareSession(cfg *Config, module, prog, runProg, launchRef, extra, argsOverride string) (*Session, error) {
	m.mu.Lock()
	for id, s := range m.sessions {
		if s.State() == StateExit {
			delete(m.sessions, id) // 已断开(close/切换/EOF)的残留,资源已释放
		}
	}
	var live *Session
	for _, s := range m.sessions {
		live = s
		break
	}
	m.mu.Unlock()

	if live != nil {
		if live.State() == StateIdle && live.sameTarget(cfg) {
			live.SetRun(cfg, module, prog, runProg, launchRef, extra)
			live.ArgsOverride = argsOverride
			return live, nil
		}
		if !live.sameTarget(cfg) {
			log.Printf("[debug] 目标切换为 %s,断开旧会话 %s 后重建", cfg.EnvName(), live.ID)
			_ = live.Close()
			m.Remove(live.ID)
		} else {
			return nil, fmt.Errorf("已存在调试会话(%s 运行中/已停站),请先结束当前调试", live.Prog)
		}
	}

	sess, err := NewSession(cfg, module, prog, runProg, launchRef, extra, m.emit)
	if err != nil {
		return nil, err
	}
	if argsOverride != "" {
		sess.ArgsOverride = argsOverride
	}
	m.mu.Lock()
	m.sessions[sess.ID] = sess
	m.mu.Unlock()
	return sess, nil
}

// resolveJob 连库按 gendbg 语义解析作业编号(gzzz_t:gzzz001 → gzzz002 实体程序 + gzzz005 模块)。
// 返回 (模块, 实体程序, err):err 仅在 SSH 连接/动态路径探测失败时非 nil;
// 未配置 db/查询失败/未命中 → ("","","","", nil),由调用方回退文件搜索。
func (m *Manager) resolveJob(module, job string) (mod, prog, launchRef, extra string, err error) {
	return m.resolveJobWith(m.cfg, module, job)
}

// resolveJobWith 同 resolveJob,但按传入 cfg(多 SSH/多区域克隆)解析。
func (m *Manager) resolveJobWith(cfg *Config, module, job string) (mod, prog, launchRef, extra string, err error) {
	if cfg.DB == nil || !reProgName.MatchString(job) {
		return "", "", "", "", nil
	}
	conn, err := host.Dial(cfg.SSH)
	if err != nil {
		return "", "", "", "", fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer conn.Close()
	zone := cfg.Zone
	if zone == "" {
		zone = "36"
	}
	// 动态路径(登录区域 → 环境脚本):按本次启动的区域探针+缓存(覆盖/多环境时不用
	// 全局 active 区域),结果写入 cfg.Runtime——预会话模块解析/42r 校验直接用真实路径。
	// 失败即报错:T100 路径只来自登录动态获取,无静态配置可回退
	if cfg.Runtime == nil || !cfg.Runtime.Valid() {
		env, perr := m.getRuntimeEnv(conn, zone)
		if perr != nil {
			return "", "", "", "", fmt.Errorf("无法获取环境 %s(zone %s)的 T100 路径: %w", cfg.EnvName(), zone, perr)
		}
		cfg.Runtime = env
	}
	// 显式连接(SSH 页 dbConn 引用)连库解析作业;失败静默,由调用方回退文件搜索
	d, err := resolveDBRun(conn, cfg)
	if err != nil {
		return "", "", "", "", nil
	}
	jr, err := dbResolveJob(conn, d, job)
	if err != nil || jr.Prog == "" {
		return "", "", "", "", nil
	}
	p := jr.Prog
	// gendbg 原版语义:gzza004 的 $变量 由选区后 shell 展开为权威 42r 路径(标准/客制都覆盖)
	if !strings.HasPrefix(jr.LaunchRef, "$") {
		jr.LaunchRef, jr.Extra = "", ""
	}
	launchRef, extra = jr.LaunchRef, jr.Extra
	if module != "" {
		return module, p, launchRef, extra, nil // 用户显式指定模块:尊重指定,仅采纳实体程序
	}
	mod = strings.ToLower(jr.Module)
	if jr.Module == "" || jr.Module == "-" || !modHas42r(conn, cfg.TopDirActual(), mod, p) {
		// 模块码缺失或目录对不上:全模块搜索实体程序的 42r;0/多命中留给会话内兜底报错
		if cands := searchModule42r(conn, cfg.ModuleRootsActual(), p); len(cands) == 1 {
			mod = cands[0]
		} else {
			mod = ""
		}
	}
	return mod, p, launchRef, extra, nil
}

// LaunchReplay 接口日志重放调试:等价 T100 日志内嵌的 `r.dg <作业> '<req>' '<rsp>'`。
// 作业取 wsfa012(gzja_t 服务→程序的解析结果),再走 gzzz_t 解析实体程序+模块;
// 报文文件被清理时用 CLOB 内容落到服务器临时文件再重放。
// 会话复用规则与普通启动一致:同目标空闲宿主直接复用。
// LaunchReplay 用日志报文重放调试。reqOverride 非空 = 界面里改过的入参(落临时文件后作为 reqPath)。
// 返回的 warn 非空表示这次重放用的是入库的截断文本,结果可能不可信(见 WriteReplayFiles)。
func (m *Manager) LaunchReplay(item *WSLogItem, content *WSLogContent, reqOverride string) (*Session, string, error) {
	job := strings.TrimSpace(item.Job)
	if job == "" {
		return nil, "", fmt.Errorf("该日志没有关联作业编号(wsfa012 为空),无法重放")
	}
	module, runProg, launchRef, extra := "", "", "", ""
	if mod2, prog2, ref2, extra2, rerr := m.resolveJob("", job); rerr != nil {
		return nil, "", rerr
	} else if prog2 != "" {
		module, runProg, launchRef, extra = mod2, prog2, ref2, extra2
	}
	conn, err := host.Dial(m.cfg.SSH)
	if err != nil {
		return nil, "", fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer conn.Close()
	reqPath, rspPath, replayWarn, err := WriteReplayFiles(conn, item, content, reqOverride)
	if err != nil {
		return nil, "", err
	}
	// 路径进 shell 单引号,剔除单引号防注入。
	// 对齐 r.dg:报文文件追加在标准参数(BBDL 会话标记等)之后,
	// cl_wss_init 依 BBDL 标记识别调试模式并重建 g_argv;只传报文会导致 g_argv 为空 → -1
	q := func(p string) string { return "'" + strings.ReplaceAll(p, "'", "") + "'" }
	args := strings.ReplaceAll(m.cfg.LaunchArgs, "{prog}", job)
	args += " " + q(reqPath)
	if rspPath != "" {
		args += " " + q(rspPath)
	}
	sess, err := m.prepareSession(m.cfg, module, job, runProg, launchRef, extra, args)
	if err != nil {
		return nil, "", err
	}
	// 新的一轮运行:清空上一轮攒下的源码副本(见 srcmirror.go);
	// "已镜像"记录必须跟着一起清(复用的宿主会话会把它带进下一轮)。
	clearMirror(m.cfg.DataDir)
	sess.resetMirrored()
	// 用了入库的截断文本重放:说清楚,别让人把"程序一路退出"当成 bug 去查
	if replayWarn != "" {
		sess.emitEvent(Event{Type: "log", Text: "⚠ " + replayWarn})
	}
	if runProg != "" && runProg != job {
		sess.emitEvent(Event{Type: "log", Text: fmt.Sprintf("重放调试:作业 %s → 实体程序 %s(gzzz_t)", job, runProg)})
	}
	if reqOverride != "" {
		sess.emitEvent(Event{Type: "log", Text: fmt.Sprintf("重放使用界面修改后的入参(%d 字节),已写入 %s", len(reqOverride), reqPath)})
	}
	sess.emitEvent(Event{Type: "log", Text: fmt.Sprintf("重放 %s:fglrun -d %s %s", item.Service, runProgOr(job, runProg), args)})
	return sess, replayWarn, nil
}

func runProgOr(job, runProg string) string {
	if runProg != "" {
		return runProg
	}
	return job
}

// Get 取会话
func (m *Manager) Get(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

// Current 返回当前唯一会话(无则 nil)
func (m *Manager) Current() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		return s
	}
	return nil
}

// Snapshot 会话列表快照
type SessionBrief struct {
	ID       string  `json:"id"`
	Module   string  `json:"module"`
	Prog     string  `json:"prog"`
	RunProg  string  `json:"runProg,omitempty"`
	State    string  `json:"state"`
	Env      string  `json:"env,omitempty"` // 会话所属环境名(设置页 envs)
	Started  bool    `json:"started"`
	File     string  `json:"file,omitempty"`
	Line     int     `json:"line,omitempty"`
	Func     string  `json:"func,omitempty"`
	Reason   string  `json:"reason,omitempty"`
	Holding  float64 `json:"holdingSeconds"`
	Breaks   int     `json:"breakpoints"`
	Watchdog int     `json:"watchdogSeconds"`
	// 停站现场就是交互语句(INPUT/MENU/DISPLAY ARRAY…):程序把控制权交给了界面。
	// 零成本判定(只看停站那一行的源码文本),供 AI 直接分支,不必再读源码做预测。
	WaitingForUser bool   `json:"waitingForUser,omitempty"`
	WaitingKind    string `json:"waitingKind,omitempty"`
	// SilentSeconds:运行态下距最近一次协议输出的秒数 —— 判断"跑了很久却毫无动静"
	// (可能就是停在界面上等用户,也可能是慢查询)。停站态为 0。
	SilentSeconds float64 `json:"silentSeconds,omitempty"`
	// Mode 谁在驾驶:solo(纯人工)| collab(协作,AI 主导)
	Mode string `json:"mode"`
	// Inflight 正在执行的那条命令(空闲为 nil)—— 界面上"AI 正在执行 continue(已 12s)"
	Inflight *InflightInfo `json:"inflight,omitempty"`
}

func (m *Manager) Snapshot() []SessionBrief {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SessionBrief, 0, len(m.sessions))
	for _, s := range m.sessions {
		cur := s.Cur()
		out = append(out, SessionBrief{
			ID: s.ID, Module: s.Module, Prog: s.Prog, RunProg: s.RunProg,
			State: string(s.State()), Env: s.EnvName(), Started: s.Started(), File: cur.File, Line: cur.Line,
			Func: cur.Func, Reason: cur.Reason,
			Holding:        s.HoldingSeconds(),
			Breaks:         len(s.Breakpoints()),
			Watchdog:       m.cfg.WatchdogSeconds,
			WaitingForUser: cur.WaitingForUser,
			WaitingKind:    cur.WaitingKind,
			SilentSeconds:  s.SilentSeconds(),
			Mode:           s.Mode(),
			Inflight:       s.Inflight(),
		})
	}
	return out
}

// Remove 移除已结束的会话
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

// CreateSessionOn 新建并登记一个纯宿主会话(module/prog 为空,登录后回到 idle),
// 供「会话重启/切换/连接」在指定目标环境上重连;调用方随后驱动 BootIdle。
func (m *Manager) CreateSessionOn(cfg *Config) (*Session, error) {
	m.mu.Lock()
	for id, s := range m.sessions {
		if s.State() == StateExit {
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()
	sess, err := NewSession(cfg, "", "", "", "", "", m.emit)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.sessions[sess.ID] = sess
	m.mu.Unlock()
	return sess, nil
}

// SourcePreview 会话建立前预取源码:读母版消除前端启动期空白,顺带落本地镜像。
// module 为空(留待会话内自动解析)时直接跳过。
func (m *Manager) SourcePreview(module, prog string) (*SourceFile, error) {
	if module == "" {
		return nil, fmt.Errorf("module 为空,跳过源码预取")
	}
	dvmFile := module + "_" + prog + ".4gl"
	var out *SourceFile
	err := m.withSrcConn(func(_ *host.SSHConn, cl *sftp.Client) error {
		var lastErr error
		for _, p := range sourceCandidatePaths(m.cfg.ModuleRootsActual(), module, dvmFile) {
			f, err := cl.Open(p)
			if err != nil {
				lastErr = err
				continue
			}
			var mt time.Time
			if fi, err := f.Stat(); err == nil {
				mt = fi.ModTime()
			}
			data, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				lastErr = err
				continue
			}
			out = &SourceFile{
				DVMFile: dvmFile, Path: p, Content: string(data), ModTime: mt,
				LocalPath: writeMirror(m.cfg.DataDir,
					mirrorEnvSeg(m.cfg.EnvName(), m.cfg.SSH.Host, m.cfg.Zone), p, data),
			}
			return nil
		}
		return fmt.Errorf("源码未找到(%s): %w", dvmFile, lastErr)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- 会话外只读能力(AI/CLI 信息通道;不经会话,独立短连接,白名单限登录区源码目录) ----------

// SourceResult 会话外源码读取结果(带行段裁剪,避免大文件整读灌满 AI 上下文)。
type SourceResult struct {
	SourceFile
	From int `json:"from"` // 1-based 返回行段起点
	To   int `json:"to"`   // 1-based 返回行段终点(含)
	All  int `json:"all"`  // 文件总行数
}

// maxSourceFileBytes 会话外读取的文件大小上限(源码一般远小于此)。
const maxSourceFileBytes = 4 << 20

// ReadSourceStandalone 读取登录区源码目录(动态 ERP/COM)内的源码文件。
//   - path 非空:直接按路径读(白名单校验源码目录前缀);
//   - path 为空:file(如 asf_bsft001_wf.4gl)按 sourceCandidatePaths 候选解析,module 可空(仅查公共目录);
//   - from/to(1-based):返回行段(from<=0 从头,to<=0 到底),行文本超长截断防注入超长行。
//
// 走会话外复用连接(srcmirror.go),顺带把整份源码落本地镜像并在结果里带出 LocalPath。
// 返回裁剪后的内容与文件总行数。
func (m *Manager) ReadSourceStandalone(module, file, path string, from, to int) (*SourceResult, error) {
	if path == "" && file == "" {
		return nil, fmt.Errorf("需要 file 或 path 参数")
	}
	var (
		data []byte
		mt   time.Time
	)
	err := m.withSrcConn(func(_ *host.SSHConn, cl *sftp.Client) error {
		roots := m.cfg.ModuleRootsActual()
		if len(roots) == 0 {
			return fmt.Errorf("登录区源码目录未知(环境路径解析失败)")
		}
		readOne := func(p string) ([]byte, time.Time, error) {
			f, err := cl.Open(p)
			if err != nil {
				return nil, time.Time{}, err
			}
			defer f.Close()
			var t time.Time
			if fi, err := f.Stat(); err == nil {
				t = fi.ModTime()
			}
			d, err := io.ReadAll(f)
			return d, t, err
		}
		// 路径白名单:必须在登录区源码目录之内
		inRoots := func(p string) bool {
			for _, root := range roots {
				if strings.HasPrefix(p, root+"/") {
					return true
				}
			}
			return false
		}
		if path != "" {
			if !inRoots(path) {
				return fmt.Errorf("路径不在登录区源码目录内: %s", path)
			}
			d, t, err := readOne(path)
			if err != nil {
				return err
			}
			data, mt = d, t
			return nil
		}
		var lastErr error
		for _, p := range sourceCandidatePaths(roots, module, file) {
			if !inRoots(p) {
				continue
			}
			d, t, e := readOne(p)
			if e == nil {
				data, mt, path = d, t, p
				return nil
			}
			lastErr = e
		}
		return fmt.Errorf("源码未找到(%s): %v", file, lastErr)
	})
	if err != nil {
		return nil, err
	}
	// 镜像落的是**整份**源码(在下面的行段裁剪与大小截断之前),本地副本要能整读
	mirrorPath := writeMirror(m.cfg.DataDir,
		mirrorEnvSeg(m.cfg.EnvName(), m.cfg.SSH.Host, m.cfg.Zone), path, data)
	if len(data) > maxSourceFileBytes {
		data = data[:maxSourceFileBytes]
	}
	lines := strings.Split(string(data), "\n")
	all := len(lines)
	// 去尾部空行(文件常见尾换行)
	for all > 0 && strings.TrimSpace(lines[all-1]) == "" {
		all--
	}
	if from <= 0 {
		from = 1
	}
	if to <= 0 || to > all {
		to = all
	}
	if from > to {
		from, to = 1, all
	}
	slice := lines[from-1 : to]
	// 单行超长截断(记录行/极长字符串防爆)
	for i, ln := range slice {
		if len(ln) > 4096 {
			slice[i] = ln[:4096] + "…(截断)"
		}
	}
	res := &SourceResult{
		SourceFile: SourceFile{DVMFile: file, Path: path, ModTime: mt, LocalPath: mirrorPath},
		From:       from, To: to, All: all,
	}
	if from <= all {
		res.Content = strings.Join(slice, "\n")
	}
	return res, nil
}

// JobInfo 作业编号 → 实体程序/模块解析结果(信息通道,不启动会话)。
type JobInfo struct {
	Prog      string `json:"prog"`                // 用户作业编号
	Module    string `json:"module"`              // 解析出的模块目录(空=未解析)
	RunProg   string `json:"runProg"`             // 实体程序(gzzz_t 解析;空=未解析)
	LaunchRef string `json:"launchRef,omitempty"` //
	Extra     string `json:"extra,omitempty"`     //
	Note      string `json:"note,omitempty"`      // 未解析/降级时的说明
}

// ResolveJob 尽力解析作业:未配置 db 或连库失败/未命中时返回 note,不报错(启动时会再次解析/兜底)。
func (m *Manager) ResolveJob(module, prog string) *JobInfo {
	out := &JobInfo{Prog: prog, Module: module}
	if m.cfg.DB == nil {
		out.Note = "config.json 未配置 debug.db,无法按 gzzz_t 解析实体程序;将按作业名直接启动"
		return out
	}
	if !reProgName.MatchString(prog) {
		out.Note = "作业名非标准标识符,跳过库解析"
		return out
	}
	// resolveJobWith 内部含 连库→gzzz_t→42r 校验;动态路径/连接失败(err)或解析失败(空串)都提示
	mod, run, ref, extra, rerr := m.resolveJobWith(m.cfg, module, prog)
	if rerr != nil {
		out.Note = "无法获取该环境 T100 路径: " + rerr.Error()
		return out
	}
	if run == "" {
		out.Note = "未能按 gzzz_t 解析(连库失败或未命中);将按作业名直接启动"
		return out
	}
	if mod != "" {
		out.Module = mod
	}
	out.RunProg, out.LaunchRef, out.Extra = run, ref, extra
	return out
}

// ---------- 会话生命周期 ----------

// CloseAll 断开全部会话(服务退出时调用)
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ss := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		ss = append(ss, s)
	}
	m.sessions = map[string]*Session{}
	m.mu.Unlock()
	for _, s := range ss {
		_ = s.Close()
	}
}
