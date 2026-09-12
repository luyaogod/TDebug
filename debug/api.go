package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"tdebug/cfgfile"
	"tdebug/dbconfig"
	"tdebug/erpdb"
	"tdebug/host"
)

// Server 本地调试服务:REST + WebSocket + Web 前端
type Server struct {
	cfg     *Config
	cfgPath string // config.json 路径(设置页写回用)
	web     fs.FS
	webSub  fs.FS  // web/dist 子文件系统(未构建前端时为 nil)
	addr    string // 实际监听地址(端口顺延时与 cfg.Listen 不同;不写回配置)
	mgr     *Manager
}

// NewServer 创建服务实例;cfgPath 为 config.json 路径(设置页写回用,可为空=只读)
func NewServer(cfg *Config, web fs.FS, cfgPath string) *Server {
	s := &Server{cfg: cfg, cfgPath: cfgPath, web: web, mgr: NewManager(cfg)}
	if web != nil {
		if sub, err := fs.Sub(web, "web/dist"); err == nil {
			if f, err := sub.Open("index.html"); err == nil {
				f.Close()
				s.webSub = sub
			}
		}
	}
	return s
}

// Run 启动 HTTP 服务(阻塞到 ctx 取消)。
// 先按 cfg.Listen 建监听;端口被占用时自动顺延到下一个空闲端口(最多尝试 maxPortTries 个),
// 实际监听地址记在 s.addr(不改 cfg.Listen,避免顺延后的端口被设置页保存写回配置)。
func (s *Server) Run(ctx context.Context) error {
	ln, addr, err := s.Listen()
	if err != nil {
		return err
	}
	fmt.Printf("[tdebug] 服务已启动  前端+API: http://%s\n", addr)
	return s.Serve(ctx, ln)
}

// maxPortTries 端口被占用时最多顺延尝试的次数。
const maxPortTries = 50

// Listen 建监听;cfg.Listen 被占用时尝试后续端口,成功后将实际地址记入 s.addr。
// 返回 listener 与实际监听地址(host:port);cfg.Listen 保持配置原值不变。
func (s *Server) Listen() (net.Listener, string, error) {
	host, portStr, err := net.SplitHostPort(s.cfg.Listen)
	if err != nil {
		return nil, "", fmt.Errorf("非法监听地址 %q: %w", s.cfg.Listen, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return nil, "", fmt.Errorf("非法监听端口 %q: %w", portStr, err)
	}
	var lastErr error
	for i := 0; i < maxPortTries; i++ {
		addr := net.JoinHostPort(host, strconv.Itoa(port+i))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			s.addr = addr
			return ln, addr, nil
		}
		lastErr = err
	}
	return nil, "", fmt.Errorf("监听 %s 失败(尝试 %d 个端口均不可用): %w",
		s.cfg.Listen, maxPortTries, lastErr)
}

// Serve 用既有 listener 提供 HTTP 服务(阻塞到 ctx 取消或连接关闭)。
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	mux := http.NewServeMux()
	s.routes(mux)
	srv := &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	err := srv.Serve(ln)
	if err == http.ErrServerClosed {
		s.mgr.CloseAll()
		return nil
	}
	return err
}

// ListenAddr 返回实际监听地址(端口顺延时与配置值不同;未启动时返回配置值)。
func (s *Server) ListenAddr() string {
	if s.addr != "" {
		return s.addr
	}
	return s.cfg.Listen
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", s.hStatus)
	mux.HandleFunc("GET /api/events", s.hEvents)
	mux.HandleFunc("GET /api/source-file", s.hSourceFile)
	mux.HandleFunc("GET /api/jobinfo", s.hJobInfo)
	mux.HandleFunc("POST /api/sessions", s.hLaunch)
	mux.HandleFunc("GET /api/sessions", s.hList)
	mux.HandleFunc("GET /api/sessions/{id}", s.hSnapshot)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.hQuit)
	mux.HandleFunc("POST /api/sessions/{id}/restart", s.hSessionRestart)
	mux.HandleFunc("POST /api/sessions/{id}/close", s.hSessionClose)
	mux.HandleFunc("POST /api/sessions/{id}/topent", s.hTopent)
	mux.HandleFunc("POST /api/sessions/switch", s.hSessionSwitch)
	mux.HandleFunc("GET /api/sessions/{id}/breakpoints", s.hBPList)
	mux.HandleFunc("POST /api/sessions/{id}/breakpoints", s.hBPAdd)
	mux.HandleFunc("DELETE /api/sessions/{id}/breakpoints/{num}", s.hBPDel)
	mux.HandleFunc("POST /api/sessions/{id}/control", s.hControl)
	mux.HandleFunc("POST /api/sessions/{id}/print", s.hPrint)
	mux.HandleFunc("POST /api/sessions/{id}/where", s.hWhere)
	mux.HandleFunc("POST /api/sessions/{id}/raw", s.hRaw)
	mux.HandleFunc("GET /api/sessions/{id}/locals", s.hLocals)
	mux.HandleFunc("GET /api/sessions/{id}/globals", s.hGlobals)
	mux.HandleFunc("GET /api/sessions/{id}/sources", s.hSources)
	mux.HandleFunc("GET /api/sessions/{id}/functions", s.hFunctions)
	mux.HandleFunc("GET /api/sessions/{id}/autovars", s.hAutovars)
	mux.HandleFunc("POST /api/sessions/{id}/autovars", s.hAutovarsSet)
	mux.HandleFunc("POST /api/sessions/{id}/frame", s.hFrame)
	mux.HandleFunc("POST /api/sessions/{id}/locate", s.hLocate)
	mux.HandleFunc("POST /api/sessions/{id}/calibrate", s.hCalibrate)
	mux.HandleFunc("POST /api/sessions/{id}/breakpoints/{num}/enabled", s.hBPEnabled)
	mux.HandleFunc("GET /api/sessions/{id}/source", s.hSource)
	mux.HandleFunc("GET /api/source-preview", s.hSourcePreview)
	mux.HandleFunc("POST /api/wstest", s.hWSTest)
	mux.HandleFunc("GET /api/wslogs", s.hWSLogs)
	mux.HandleFunc("GET /api/wslogs/content", s.hWSLogContent)
	mux.HandleFunc("POST /api/wslogs/debug", s.hWSLogDebug)
	mux.HandleFunc("GET /api/settings", s.hSettingsGet)
	mux.HandleFunc("PUT /api/settings", s.hSettingsPut)
	mux.HandleFunc("POST /api/dbprobe", s.hDBProbe)
	mux.HandleFunc("POST /api/dbaccverify", s.hDbAccVerify)
	mux.HandleFunc("POST /api/conntest", s.hConnTest)
	mux.HandleFunc("GET /api/ws", s.hWS)
	mux.HandleFunc("/", s.hStatic)
}

// ---------- 工具 ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]any{"ok": false, "error": err.Error()})
}

func readBody[T any](w http.ResponseWriter, r *http.Request, out *T) bool {
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		fail(w, 400, fmt.Errorf("请求体解析失败: %v", err))
		return false
	}
	return true
}

func (s *Server) sessOf(w http.ResponseWriter, r *http.Request) *Session {
	id := r.PathValue("id")
	sess := s.mgr.Get(id)
	if sess == nil {
		fail(w, 404, fmt.Errorf("会话不存在: %s", id))
	}
	return sess
}

// ---------- 会话 ----------

func (s *Server) hStatus(w http.ResponseWriter, r *http.Request) {
	m := map[string]any{
		"server":   "tdebug-debug",
		"ssh":      s.cfg.SSH.Host,
		"zone":     s.cfg.Zone,
		"listen":   s.ListenAddr(),
		"watchdog": s.cfg.WatchdogSeconds,
	}
	// topDir 只在登录动态获取后才有意义(未登录无静态值);无会话/未探测时省略
	if top := s.cfg.TopDirActual(); top != "" {
		m["topDir"] = top
	}
	writeJSON(w, 200, m)
}

// hEvents 最近会话事件(tail):GET /api/events?tail=N,供 AI/CLI 观察"发生了什么"。
func (s *Server) hEvents(w http.ResponseWriter, r *http.Request) {
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	writeJSON(w, 200, map[string]any{"ok": true, "events": s.mgr.Events(tail)})
}

// hSourceFile 会话外白名单源码读取:GET /api/source-file?module=&file=&path=&from=&to=
// (AI 信息通道;独立短连接,不经会话,路径限制在登录区源码目录)
func (s *Server) hSourceFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, _ := strconv.Atoi(q.Get("from"))
	to, _ := strconv.Atoi(q.Get("to"))
	res, err := s.mgr.ReadSourceStandalone(q.Get("module"), q.Get("file"), q.Get("path"), from, to)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "source": res})
}

// hJobInfo 作业→实体程序/模块解析:GET /api/jobinfo?prog=&module= (不启动会话)
func (s *Server) hJobInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prog := strings.TrimSpace(q.Get("prog"))
	if prog == "" {
		fail(w, 400, fmt.Errorf("需要 prog 参数"))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "job": s.mgr.ResolveJob(strings.TrimSpace(q.Get("module")), prog)})
}

func (s *Server) hLaunch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Module string `json:"module"`
		Prog   string `json:"prog"`
		SSH    string `json:"ssh"`  // 多 SSH 配置名(设置页维护);空 = 默认连接
		Zone   string `json:"zone"` // 区域覆盖(31/35/36/39/t);空 = 默认区域
	}
	if !readBody(w, r, &req) {
		return
	}
	if req.Prog == "" {
		fail(w, 400, fmt.Errorf("prog 必填(module 可留空,自动按作业名解析)"))
		return
	}
	// 按名换 SSH / 覆盖区域:克隆配置,默认链路零影响
	cfg := s.cfg
	if req.SSH != "" || req.Zone != "" {
		c2 := *s.cfg
		if req.SSH != "" {
			c2.SSH = s.cfg.SSHByName(req.SSH)
		}
		c2 = *c2.CloneWithZone(req.Zone)
		cfg = &c2
	}
	sess, err := s.mgr.LaunchWith(cfg, req.Module, req.Prog)
	if err != nil {
		fail(w, 409, err)
		return
	}
	go func() {
		if err := sess.Launch(r.Context()); err != nil {
			log.Printf("[debug] 会话 %s(%s) 启动失败: %v", sess.ID, sess.Prog, err)
			s.mgr.emit(Event{Type: "log", SessionID: sess.ID, Text: "启动失败: " + err.Error()})
			if sess.Booted() {
				// 复用宿主上启动失败:Launch 已把会话收回空闲(可换作业重试/重启会话);
				// 仅在异常退回 exit 时移除,避免卡在 loading
				if sess.State() == StateExit {
					s.mgr.Remove(sess.ID)
				}
			} else {
				// 首次登录失败:宿主不可用,终结并移除,避免卡在 loading 挡住下一次启动
				sess.ForceExit()
				s.mgr.Remove(sess.ID)
			}
		}
	}()
	// runProg: gzzz_t 解析出的实体程序(前端源码命名/预取要用它,而不是作业编号)
	writeJSON(w, 200, map[string]any{"ok": true, "sessionId": sess.ID,
		"module": sess.Module, "prog": sess.Prog, "runProg": sess.RunProg})
}

func (s *Server) hList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "sessions": s.mgr.Snapshot()})
}

func (s *Server) hSnapshot(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	cur := sess.Cur()
	writeJSON(w, 200, map[string]any{
		"ok": true,
		"id": sess.ID, "module": sess.Module, "prog": sess.Prog, "runProg": sess.RunProg,
		"env":   sess.EnvName(),
		"state": string(sess.State()), "stop": cur,
		"started":         sess.Started(),
		"breakpoints":     sess.Breakpoints(),
		"holdingSeconds":  sess.HoldingSeconds(),
		"watchdogSeconds": s.cfg.WatchdogSeconds,
		"topent":          sess.TopentOverride(),
		"topentCfg":       sess.TopentCfg(),
	})
}

func (s *Server) hQuit(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	// 「结束调试」只结束本轮运行,宿主会话保留(idle);再次启动免重新登录。
	// 卡死降级整体断开时(StateExit)才移除会话。
	if err := sess.EndRun(); err != nil {
		fail(w, 500, err)
		return
	}
	kept := sess.State() != StateExit
	if !kept {
		s.mgr.Remove(sess.ID)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "kept": kept, "state": "idle"})
}

// ---------- 会话管理(单一常驻会话的切换/重启/结束) ----------

// envCfgFor 取指定环境的生效配置(CloneEnv);若与当前生效目标同名(或未配置 envs 列表)则用当前配置快照
func (s *Server) envCfgFor(name string) *Config {
	if c := s.cfg.CloneEnv(name); c != nil {
		return c
	}
	if name != "" && s.cfg.EnvName() == name {
		cc := *s.cfg
		return &cc
	}
	return nil
}

// selectEnv 把当前环境切到 cfg 所属环境并热生效(仅内存,不写 config.json)。
// 当前环境是运行时概念:由会话切换/CLI env 命令确定,服务重启后回到 sshs 首条。
func (s *Server) selectEnv(cfg *Config) {
	if cfg != nil {
		*s.cfg = *cfg
	}
}

// bootToIdle 异步登录新会话到 idle(切换/重启用),失败终结并移除
func (s *Server) bootToIdle(ns *Session, ctx context.Context) {
	if err := ns.BootIdle(ctx); err != nil {
		log.Printf("[debug] 会话 %s(%s) 登录失败: %v", ns.ID, ns.EnvName(), err)
		s.mgr.emit(Event{Type: "log", SessionID: ns.ID, Text: "会话连接失败: " + err.Error()})
		ns.ForceExit()
		s.mgr.Remove(ns.ID)
	}
}

// hSessionClose 「结束会话」:结束当前 debug 并彻底断开连接(与设置默认解耦)
func (s *Server) hSessionClose(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	if err := sess.Close(); err != nil {
		fail(w, 500, err)
		return
	}
	s.mgr.Remove(sess.ID)
	writeJSON(w, 200, map[string]any{"ok": true, "state": "exit"})
}

// hSessionRestart 「重启会话」:断开并重连同环境,回到 idle 待启动
func (s *Server) hSessionRestart(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	env := sess.EnvName()
	id := sess.ID
	// 先收口当前会话(运行中会先中断/quit),再重建连接
	if err := sess.Close(); err != nil {
		fail(w, 500, err)
		return
	}
	s.mgr.Remove(id)
	cfg := s.envCfgFor(env)
	if cfg == nil {
		cfg = s.envCfgFor(s.cfg.EnvName()) // 原环境已被删除:退回当前默认
	}
	if cfg == nil {
		fail(w, 500, fmt.Errorf("找不到可用的连接配置(环境 %q 已被删除)", env))
		return
	}
	ns, err := s.mgr.CreateSessionOn(cfg)
	if err != nil {
		fail(w, 502, err)
		return
	}
	s.mgr.emit(Event{Type: "log", Text: fmt.Sprintf("重启会话:重新连接 %s …", cfg.EnvName())})
	go s.bootToIdle(ns, r.Context())
	writeJSON(w, 200, map[string]any{"ok": true, "sessionId": ns.ID, "env": cfg.EnvName(), "state": "loading"})
}

// hTopent 空闲态重新设置 TOPENT(会话内,下一轮调试生效)。
// 仅允许 idle(宿主 shell 就绪、无调试运行);值不限数字/文本(导出为环境变量),
// 服务端剔除两侧空白;留空 = 清除手动设置回到配置/登录默认。
func (s *Server) hTopent(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if !readBody(w, r, &req) {
		return
	}
	req.Value = trimTopent(req.Value)
	if st := sess.State(); st != StateIdle {
		fail(w, 409, fmt.Errorf("仅会话空闲时可设置 TOPENT(当前 %s),请先结束当前调试", st))
		return
	}
	if err := sess.SetTopent(req.Value); err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "topent": req.Value})
}

// trimTopent TOPENT 归一:剔除两侧空白(值不限数字/文本);全空白归一为空(清除)
func trimTopent(v string) string {
	return strings.TrimSpace(v)
}

// hSessionSwitch 「切换会话」:把当前环境切到目标环境(仅内存)并按该环境重连到 idle。
// 若已是目标环境会话则幂等返回(不改动正在进行的调试)。
func (s *Server) hSessionSwitch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Env string `json:"env"`
	}
	if !readBody(w, r, &req) {
		return
	}
	cfg := s.envCfgFor(req.Env)
	if cfg == nil {
		fail(w, 404, fmt.Errorf("环境 %q 不存在(请在设置中添加)", req.Env))
		return
	}
	if s.cfg.EnvName() != req.Env {
		s.selectEnv(cfg)
	}
	cur := s.mgr.Current()
	if cur != nil {
		if cur.sameTarget(cfg) {
			writeJSON(w, 200, map[string]any{"ok": true, "sessionId": cur.ID, "env": req.Env, "state": string(cur.State())})
			return
		}
		s.mgr.emit(Event{Type: "log", SessionID: cur.ID, Text: fmt.Sprintf("切换会话到 %s,断开当前会话", req.Env)})
		_ = cur.Close()
		s.mgr.Remove(cur.ID)
	}
	ns, err := s.mgr.CreateSessionOn(cfg)
	if err != nil {
		fail(w, 502, err)
		return
	}
	s.mgr.emit(Event{Type: "log", Text: fmt.Sprintf("切换会话:正在连接 %s …", req.Env)})
	go s.bootToIdle(ns, r.Context())
	writeJSON(w, 200, map[string]any{"ok": true, "sessionId": ns.ID, "env": req.Env, "state": "loading"})
}

// ---------- 断点 ----------

func (s *Server) hBPList(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "breakpoints": sess.Breakpoints()})
}

func (s *Server) hBPAdd(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	var req struct {
		Location string `json:"location"` // 行号 / 函数名 / file:line
	}
	if !readBody(w, r, &req) {
		return
	}
	log.Println("[bp-add] session=" + sess.ID + " loc=" + req.Location)
	bp, err := sess.Break(req.Location)
	if err != nil {
		fail(w, 400, err)
		return
	}
	log.Printf("[bp-add] result #%d %s:%d", bp.Num, bp.File, bp.Line)
	writeJSON(w, 200, map[string]any{"ok": true, "breakpoint": bp})
}

func (s *Server) hBPDel(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	num, err := strconv.Atoi(r.PathValue("num"))
	if err != nil {
		fail(w, 400, fmt.Errorf("断点编号无效"))
		return
	}
	log.Println("[bp-del] session=" + sess.ID + " num=" + strconv.Itoa(num))
	if err := sess.DeleteBreakpoint(num); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- 控制 / 求值 ----------

func (s *Server) hControl(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	var req struct {
		Action string `json:"action"` // continue|run|next|step|finish|until|interrupt
		Arg    string `json:"arg,omitempty"`
	}
	if !readBody(w, r, &req) {
		return
	}
	var stop *StopInfo
	var err error
	switch req.Action {
	case "continue":
		_, err = sess.Continue()
	case "run":
		_, err = sess.Run()
	case "next", "step", "finish":
		stop, err = sess.Step(req.Action)
	case "until":
		loc := req.Arg
		if loc == "" {
			stop, err = sess.Step("until")
		} else {
			stop, err = sess.Step("until " + loc)
		}
	case "interrupt":
		err = sess.Interrupt()
	default:
		fail(w, 400, fmt.Errorf("未知 action: %s", req.Action))
		return
	}
	if err != nil {
		fail(w, 409, err)
		return
	}
	resp := map[string]any{"ok": true, "state": string(sess.State())}
	if stop != nil {
		resp["stop"] = stop
	}
	writeJSON(w, 200, resp)
}

func (s *Server) hPrint(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	var req struct {
		Expr string `json:"expr"`
	}
	if !readBody(w, r, &req) {
		return
	}
	v, err := sess.Print(req.Expr)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "value": v})
}

func (s *Server) hWhere(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	frames, err := sess.Where()
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "frames": frames})
}

func (s *Server) hRaw(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	var req struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"` // 等待命令完成的秒数(0=默认30);continue/until 等长命令可调大
	}
	if !readBody(w, r, &req) {
		return
	}
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" || strings.ContainsAny(cmd, "\r\n") {
		fail(w, 400, fmt.Errorf("命令不能为空或含换行"))
		return
	}
	lower := strings.ToLower(cmd)
	if lower == "quit" || lower == "run" || strings.HasPrefix(lower, "run ") {
		fail(w, 400, fmt.Errorf("请使用会话控制接口执行 quit/run"))
		return
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 30
	}
	if timeout > 600 {
		timeout = 600
	}
	lines, err := sess.Raw(cmd, time.Duration(timeout)*time.Second)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "lines": lines})
}

// ---------- 上下文查询 ----------

func (s *Server) hLocals(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	vars, err := sess.Locals()
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "vars": vars})
}

func (s *Server) hGlobals(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	vars, total, err := sess.Globals(limit)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "vars": vars, "total": total})
}

// hLocate POST /api/sessions/{id}/locate {word}:定位函数到源文件与行号。
// 用 fgldb info line [module.]function(BDL 文档语法),仅停站状态可用;Ctrl+点击跳函数用
func (s *Server) hLocate(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	var req struct {
		Word string `json:"word"`
	}
	if !readBody(w, r, &req) {
		return
	}
	word := strings.TrimSpace(req.Word)
	if !reIdent.MatchString(word) {
		fail(w, 400, fmt.Errorf("函数名非法: %q", word))
		return
	}
	file, line, err := sess.InfoLine(word)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "file": file, "line": line})
}

// hCalibrate 行号校准:停站后检测 fgldb(DVM)行号与磁盘源码的偏移量,
// 供前端把 Monaco 行号对齐到协议流。POST /api/sessions/{id}/calibrate
func (s *Server) hCalibrate(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	offset, err := sess.CalibrateOffset()
	if err != nil {
		fail(w, 400, err)
		return
	}
	log.Printf("[calibrate] session=%s offset=%d", sess.ID, offset)
	writeJSON(w, 200, map[string]any{"ok": true, "offset": offset})
}

func (s *Server) hSources(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	sources, err := sess.Sources()
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "sources": sources})
}

func (s *Server) hFunctions(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	fns, total, err := sess.Functions(limit)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "functions": fns, "total": total})
}

func (s *Server) hAutovars(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "vars": sess.Autovars()})
}

// hAutovarsSet POST /api/sessions/{id}/autovars {auto}:开关停站后自动求值自动变量
// (默认关;开启且已停站时立即补一次求值,结果照常以 autovars 事件推送)
func (s *Server) hAutovarsSet(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	var req struct {
		Auto *bool `json:"auto"`
	}
	if !readBody(w, r, &req) || req.Auto == nil {
		return
	}
	sess.SetAutovarsOn(*req.Auto)
	writeJSON(w, 200, map[string]any{"ok": true, "auto": *req.Auto})
}

func (s *Server) hFrame(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	var req struct {
		Num int `json:"num"`
	}
	if !readBody(w, r, &req) {
		return
	}
	if err := sess.Frame(req.Num); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "frame": sess.CurFrame()})
}

func (s *Server) hBPEnabled(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	num, err := strconv.Atoi(r.PathValue("num"))
	if err != nil {
		fail(w, 400, fmt.Errorf("断点编号无效"))
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !readBody(w, r, &req) || req.Enabled == nil {
		return
	}
	if err := sess.SetBPEnabled(num, *req.Enabled); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- 源码 ----------

// hSourcePreview 会话建立前预取源码(消除启动期空白):GET /api/source-preview?module=&prog=
func (s *Server) hSourcePreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	module, prog := q.Get("module"), q.Get("prog")
	if module == "" || prog == "" {
		fail(w, 400, fmt.Errorf("需要 module 与 prog 参数"))
		return
	}
	sf, err := s.mgr.SourcePreview(module, prog)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "source": sf})
}

func (s *Server) hSource(w http.ResponseWriter, r *http.Request) {
	sess := s.sessOf(w, r)
	if sess == nil {
		return
	}
	q := r.URL.Query()
	var sf *SourceFile
	var err error
	switch {
	case q.Get("path") != "":
		sf, err = sess.ReadPath(q.Get("path"))
	case q.Get("file") != "":
		sf, err = sess.ResolveSource(q.Get("file"), q.Get("module"))
	default:
		err = fmt.Errorf("需要 file 或 path 参数")
	}
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "source": sf})
}

// ---------- 接口服务测试(复刻 awsq990 集成服务测试) ----------

// hWSTest POST /api/wstest {mode,url,body,soap} → 经服务器 curl 调用接口
func (s *Server) hWSTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
		URL  string `json:"url"`
		Body string `json:"body"`
		Soap bool   `json:"soap"`
	}
	if !readBody(w, r, &req) {
		return
	}
	if req.URL == "" {
		req.URL = WSDefaultURLFor(s.cfg, req.Mode)
	}
	conn, err := host.Dial(s.cfg.SSH)
	if err != nil {
		fail(w, 500, fmt.Errorf("SSH 连接失败: %w", err))
		return
	}
	defer conn.Close()
	res, err := WSTest(conn, req.URL, req.Body, req.Soap, 60)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "result": res})
}

// ---------- 接口日志(wsfa_t) ----------

// wsZone 返回查询用 zone(空配置默认 36)
func (s *Server) wsZone() string {
	if s.cfg.Zone == "" {
		return "36"
	}
	return s.cfg.Zone
}

// hWSLogs GET /api/wslogs?service=&onlyFail=1&limit=200
func (s *Server) hWSLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	pageSize, _ := strconv.Atoi(q.Get("pageSize"))
	conn, err := host.Dial(s.cfg.SSH)
	if err != nil {
		fail(w, 500, fmt.Errorf("SSH 连接失败: %w", err))
		return
	}
	defer conn.Close()
	dbc, err := resolveDBRun(conn, s.cfg)
	if err != nil {
		fail(w, 500, err)
		return
	}
	items, hasMore, err := listWSLogs(conn, dbc, WSLogFilter{
		Service:   q.Get("service"),
		OnlyFail:  q.Get("onlyFail") == "1",
		StartFrom: q.Get("startFrom"),
		StartTo:   q.Get("startTo"),
		Page:      page,
		PageSize:  pageSize,
	})
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": items, "hasMore": hasMore})
}

// hWSLogContent GET /api/wslogs/content?rowid=
func (s *Server) hWSLogContent(w http.ResponseWriter, r *http.Request) {
	rowid := r.URL.Query().Get("rowid")
	conn, err := host.Dial(s.cfg.SSH)
	if err != nil {
		fail(w, 500, fmt.Errorf("SSH 连接失败: %w", err))
		return
	}
	defer conn.Close()
	dbc, err := resolveDBRun(conn, s.cfg)
	if err != nil {
		fail(w, 500, err)
		return
	}
	item, content, err := WSLogDetail(conn, dbc, rowid)
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "item": item, "content": content})
}

// hWSLogDebug POST /api/wslogs/debug {rowid} → 用该日志的报文重放调试
func (s *Server) hWSLogDebug(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RowID string `json:"rowid"`
	}
	if !readBody(w, r, &req) {
		return
	}
	conn, err := host.Dial(s.cfg.SSH)
	if err != nil {
		fail(w, 500, fmt.Errorf("SSH 连接失败: %w", err))
		return
	}
	dbc, err := resolveDBRun(conn, s.cfg)
	if err != nil {
		conn.Close()
		fail(w, 500, err)
		return
	}
	item, content, err := WSLogDetail(conn, dbc, req.RowID)
	conn.Close()
	if err != nil {
		fail(w, 500, err)
		return
	}
	// 已有会话先收口:同目标(可复用空闲宿主)只结束本轮运行;不同目标直接断开。
	// 重放调试独占调试通道,旧会话不再有用。
	if cur := s.mgr.Current(); cur != nil {
		if cur.sameTarget(s.cfg) {
			s.mgr.emit(Event{Type: "log", SessionID: cur.ID, Text: "重放调试启动,结束当前调试(会话保留)"})
			_ = cur.EndRun()
			// EndRun 卡死时降级为 Close(StateExit),残留会话由 prepareSession 清扫
		} else {
			s.mgr.emit(Event{Type: "log", SessionID: cur.ID, Text: "重放调试启动,断开当前会话"})
			_ = cur.Close()
			s.mgr.Remove(cur.ID)
		}
	}
	sess, err := s.mgr.LaunchReplay(item, content)
	if err != nil {
		fail(w, 409, err)
		return
	}
	go func() {
		if err := sess.Launch(r.Context()); err != nil {
			s.mgr.emit(Event{Type: "log", SessionID: sess.ID, Text: "启动失败: " + err.Error()})
			if sess.Booted() {
				if sess.State() == StateExit {
					s.mgr.Remove(sess.ID)
				}
			} else {
				sess.ForceExit()
				s.mgr.Remove(sess.ID)
			}
		}
	}()
	writeJSON(w, 200, map[string]any{"ok": true, "sessionId": sess.ID,
		"module": sess.Module, "prog": sess.Prog, "runProg": sess.RunProg})
}

// ---------- 设置(多 SSH / 多数据库) ----------

// hSettingsGet 返回完整 debug 配置节
func (s *Server) hSettingsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.cfg)
}

// hDBProbe 设置页「自动获取数据库配置」:SSH 上服务器探测连接要素(只读)
func (s *Server) hDBProbe(w http.ResponseWriter, r *http.Request) {
	var req host.DBProbeReq
	if !readBody(w, r, &req) {
		return
	}
	if req.Host == "" || req.User == "" {
		fail(w, 400, fmt.Errorf("请先填写 SSH 主机与账号"))
		return
	}
	if req.Type == "" {
		req.Type = "oracle"
	}
	out, err := host.ProbeDBConfig(req)
	if err != nil {
		fail(w, 502, err)
		return
	}
	writeJSON(w, 200, out)
}

// hDbAccVerify 设置页账号清单「验证」:SSH 上服务器以 账号/密码 连库 select 1(只读)
func (s *Server) hDbAccVerify(w http.ResponseWriter, r *http.Request) {
	var req host.DBAccVerifyReq
	if !readBody(w, r, &req) {
		return
	}
	if req.Host == "" || req.User == "" {
		fail(w, 400, fmt.Errorf("请先填写 SSH 主机与账号"))
		return
	}
	if req.Account == "" {
		fail(w, 400, fmt.Errorf("账号不能为空"))
		return
	}
	if req.Type == "" {
		req.Type = "oracle"
	}
	if err := host.VerifyDBAcct(req); err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// hSettingsPut 写回设置:仅替换 config.json 顶层 "debug" 键(其它键如 dbconfig 原样保留),
// 并原位热更新运行中的 s.cfg(Manager 等持有同一指针,立即生效)
func (s *Server) hSettingsPut(w http.ResponseWriter, r *http.Request) {
	if s.cfgPath == "" {
		fail(w, 500, fmt.Errorf("服务未挂接 config.json 路径,无法保存"))
		return
	}
	var nc Config
	if !readBody(w, r, &nc) {
		return
	}
	nc.DataDir = s.cfg.DataDir // 运行时注入字段,请求体不带
	nc.envName = s.cfg.envName // 当前环境是运行时字段,不随设置保存改变
	nc.ApplyDefaultEnv()       // 当前环境的连接/参数合并到运行时字段
	nc.fillDefaults()

	if err := cfgfile.Edit(s.cfgPath, nil, func(root map[string]any) error {
		root["debug"] = &nc
		return nil
	}); err != nil {
		fail(w, 500, err)
		return
	}
	*s.cfg = nc // 热生效(Listen 需重启服务才换端口,其余字段即时)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// hConnTest 客户端直连测试(设置页 DB 表单「测试连接」):请求体为该 db 的配置
// (含 accounts),凭据取账号列表首项(与 db ping 同链路 erpdb/live)。
func (s *Server) hConnTest(w http.ResponseWriter, r *http.Request) {
	var req dbconfig.Connection
	if !readBody(w, r, &req) {
		return
	}
	if req.Host == "" {
		fail(w, 400, fmt.Errorf("请填写主机地址"))
		return
	}
	if len(req.Accounts) == 0 || req.Accounts[0].Account == "" {
		fail(w, 400, fmt.Errorf("请先在账号列表中添加账号(直连取列表首项)"))
		return
	}
	if req.Type == "" {
		req.Type = "oracle"
	}
	if req.Type != "oracle" && req.Type != "kingbase" {
		fail(w, 400, fmt.Errorf("类型非法: %s(仅 oracle/kingbase)", req.Type))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	conn, err := erpdb.Open(ctx, req)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer conn.Close()
	ver, err := conn.ServerVersion(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "version": ver})
}

// ---------- WebSocket ----------

func (s *Server) hWS(w http.ResponseWriter, r *http.Request) {
	ch, cancel := s.mgr.Subscribe("ws")
	defer cancel()
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	ctx := r.Context()
	for {
		select {
		case ev := <-ch:
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
			err = conn.Write(wctx, websocket.MessageText, data)
			wcancel()
			if err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) hStatic(w http.ResponseWriter, r *http.Request) {
	if s.webSub == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html lang="zh"><meta charset="utf-8">
<title>tdebug debug</title><body style="font-family:system-ui;padding:40px;line-height:1.8">
<h2>tdebug server 已运行</h2>
<p>前端尚未构建。请在 web/ 目录执行 <code>npm install &amp;&amp; npm run build</code> 后重启服务。</p>
<p>API 已可用:<code>/api/status</code>、<code>/api/ws</code>、<code>/api/sessions</code> …</p></body>`)
		return
	}
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	f, err := s.webSub.Open(strings.TrimPrefix(path, "/"))
	if err != nil {
		// SPA 兜底:未命中路径回 index.html
		f2, err2 := s.webSub.Open("index.html")
		if err2 != nil {
			http.NotFound(w, r)
			return
		}
		f = f2
		path = "/index.html"
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if st.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, path, st.ModTime(), f.(readSeeker))
}

type readSeeker interface {
	Read(p []byte) (int, error)
	Seek(offset int64, whence int) (int64, error)
}
