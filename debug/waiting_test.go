package debug

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---------- 交互语句分类 ----------

// ClassifyInteractiveLine 决定 waiting_for_user 的准确性,是"程序在等用户还是在空转"
// 这条判断链的根。用官方文档里出现过的真实行 + 几类陷阱做表驱动。
func TestClassifyInteractiveLine(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		// 官方文档实证的两行(attach 示例 / Ctrl-C 示例)
		{"DISPLAY ARRAY contlist TO sr.*", "display_array"},
		{`MENU "Test"`, "menu"},
		// 其余交互语句
		{"INPUT BY NAME s_name", "input_by_name"},
		{"INPUT ARRAY arr TO sr.*", "input_array"},
		{"INPUT s_name", "input"},
		{"CONSTRUCT BY NAME qry ON a, b", "construct"},
		// T100 客制里最常见的交互语句(真机验证时程序正停在这一行上)
		{"DIALOG ATTRIBUTES(UNBUFFERED,FIELD ORDER FORM)", "dialog"},
		{"      DIALOG", "dialog"},
		{"END DIALOG", ""}, // 只认行首关键字
		{`PROMPT "请输入单号: " FOR CHAR doc`, "prompt"},
		{"OPEN WINDOW w1 AT 1,1", "window"},
		// 前缀缩进不影响判定(停站源码行本身带缩进)
		{"      INPUT BY NAME p_cust", "input_by_name"},
		// 非交互
		{"LET i = 1", ""},
		{`CALL cl_ap_init("asf","")`, ""},
		{"END MENU", ""},  // 只认行首关键字
		{"EXIT MENU", ""}, // 同上
		{"", ""},
		{"   ", ""},
		// 陷阱:字符串里的关键字不算
		{`DISPLAY "INPUT"`, ""},
		{`LET s = 'MENU'`, ""},
		// 陷阱:预处理指令 / 注释里的关键字不算
		{"#add-point:main段define INPUT", ""},
		{"-- INPUT x", ""},
		{"LET x = 1  -- 后面跟 MENU 字样", ""},
	}
	for _, c := range cases {
		if got := ClassifyInteractiveLine(c.line); got != c.want {
			t.Errorf("ClassifyInteractiveLine(%q) = %q,期望 %q", c.line, got, c.want)
		}
	}
}

func TestCurSourceText(t *testing.T) {
	src := []SourceLine{
		{Num: 110, Text: "  LET x = 1"},
		{Num: 113, Text: "  CALL cl_ap_init()", IsCur: true},
	}
	if got := CurSourceText(src); got != "  CALL cl_ap_init()" {
		t.Fatalf("应取 IsCur 那行,实际 %q", got)
	}
	if got := CurSourceText([]SourceLine{{Num: 1, Text: "x"}}); got != "" {
		t.Fatalf("无 IsCur 应返回空串,实际 %q", got)
	}
	if got := CurSourceText(nil); got != "" {
		t.Fatalf("nil 应返回空串,实际 %q", got)
	}
}

// ---------- 软等待:放弃等待 ≠ 取消命令 ----------

func waitTestSession() *Session {
	c := cfgFor("h1", "35")
	c.WatchdogSeconds = 0 // 关掉看门狗,免得测试里冒出定时器
	return &Session{
		ID:       "s1",
		cfg:      c,
		state:    StateStopped,
		bps:      map[int]*Breakpoint{},
		srcCache: map[string]srcCacheEntry{},
	}
}

func collectEvents(s *Session) *[]Event {
	evs := &[]Event{}
	s.emit = func(ev Event) { *evs = append(*evs, ev) }
	return evs
}

func hasEvent(evs []Event, typ string) bool {
	for _, ev := range evs {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

// 软等待放弃后,状态必须如实翻成 running。
// 否则下一条 exec 会在 state==Stopped 的假象下把命令写进正在运行程序的输入缓冲,
// 在下一个提示符处被消费 = 延迟命令注入。
func TestAbandonPendingFlipsRunning(t *testing.T) {
	s := waitTestSession()
	evs := collectEvents(s)
	p := &pendingCmd{cmd: "continue", res: make(chan *execResult, 1)}
	s.pending = p

	if !s.abandonPending(p) {
		t.Fatal("槽位是自己时应标记成功")
	}
	if s.state != StateRunning {
		t.Fatalf("放弃等待后 state 应翻成 running,实际 %s", s.state)
	}
	if !p.softAbandoned {
		t.Fatal("应置 softAbandoned")
	}
	if s.pending != p {
		t.Fatal("槽位不可以在 abandonPending 里清:命令仍在飞,onStop 还要靠它区分 sync/async")
	}
	if !hasEvent(*evs, "state") {
		t.Fatal("状态变化应发出 state 事件")
	}
	if s.RunningSince().IsZero() {
		t.Fatal("翻 running 时应记录 runningSince")
	}
}

// 槽位不是这条命令时不得误标(避免把别人的命令标成已放弃)
func TestAbandonPendingStaleReturnsFalse(t *testing.T) {
	s := waitTestSession()
	other := &pendingCmd{cmd: "print", res: make(chan *execResult, 1)}
	if s.abandonPending(other) {
		t.Fatal("槽位不是这条命令时应返回 false")
	}
	if s.state != StateStopped {
		t.Fatalf("未标记成功不应改状态,实际 %s", s.state)
	}
}

// 【关键竞态】软等待放弃后停站到来:onStop 必须以锁内实况复核,走**异步**分支。
// sync 分支只回填响应、不置 stopped、不发 stopped 事件就直接 return ——
// 一旦让已无人等待的停站误走 sync,会话会永久卡在 running 且停站事件永久丢失。
func TestOnStopAfterSoftAbandonGoesAsync(t *testing.T) {
	s := waitTestSession()
	evs := collectEvents(s)
	p := &pendingCmd{cmd: "continue", res: make(chan *execResult, 1)}
	s.pending = p
	if !s.abandonPending(p) {
		t.Fatal("abandonPending 应成功")
	}

	stop := &StopInfo{Reason: "breakpoint", File: "a.4gl", Line: 10}
	// 故意传"陈旧快照":onLine 取快照与调用 onStop 之间有窗口,可能已被放弃
	s.onStop(stop, p, false)

	if got := s.State(); got != StateStopped {
		t.Fatalf("被放弃的命令误走 sync 分支:state 应回到 stopped,实际 %s", got)
	}
	if !hasEvent(*evs, "stopped") {
		t.Fatal("停站事件必须发出 —— 这正是 sync 分支漏掉它的致命路径")
	}
	if s.pending != nil {
		t.Fatal("被放弃的槽位应在 onStop 里清掉")
	}
}

// 回归:仍有活跃等待者时走 sync 分支 —— 回填响应、清槽、状态保持 stopped,
// **并且必须广播 stopped 事件**。
//
// 最后这条是本轮新增的语义。AI 发起的 next/continue 走的正是 sync 路径,
// 以前只有异步路径发事件,于是浏览器收不到任何通知、代码画面不会跟随
// (人类自己的步进靠 HTTP 响应里的 stop 落位,所以这个缺口一直没暴露)。
func TestOnStopLivePendingStaysSync(t *testing.T) {
	s := waitTestSession()
	evs := collectEvents(s)
	p := &pendingCmd{cmd: "next", res: make(chan *execResult, 1)}
	s.pending = p

	stop := &StopInfo{Reason: "step", File: "a.4gl", Line: 11}
	s.onStop(stop, p, false)

	select {
	case r := <-p.res:
		if r.Stop == nil {
			t.Fatal("sync 分支应把停站回填进命令响应")
		}
	default:
		t.Fatal("活跃 pending 应走 sync 分支并回填响应")
	}
	if s.pending != nil {
		t.Fatal("sync 分支应清槽")
	}
	if s.State() != StateStopped {
		t.Fatalf("sync 分支不迁移状态,应保持 stopped,实际 %s", s.State())
	}
	if !hasEvent(*evs, "stopped") {
		t.Fatal("sync 分支必须发 stopped 事件 —— 否则 AI 的步进/继续在界面上完全不可见")
	}
}

// ---------- 静默时长 ----------

func TestSilentSeconds(t *testing.T) {
	s := waitTestSession()
	if got := s.SilentSeconds(); got != 0 {
		t.Fatalf("停站态应为 0,实际 %v", got)
	}
	if !s.RunningSince().IsZero() {
		t.Fatal("停站态 RunningSince 应为零值")
	}

	s.state = StateRunning
	s.runningSince = time.Now().Add(-30 * time.Second)
	if got := s.SilentSeconds(); got < 29 || got > 32 {
		t.Fatalf("无输出时应退回 runningSince(约 30s),实际 %v", got)
	}

	// 有过更晚的输出:以输出时刻为准,而不是开始运行的时刻
	s.lastOutputAt = time.Now().Add(-5 * time.Second)
	if got := s.SilentSeconds(); got < 4 || got > 7 {
		t.Fatalf("应以最近输出时刻为准(约 5s),实际 %v", got)
	}
}

// ---------- why ----------

// 本来就停在断点上的程序,不能因为一次 why 被放跑 —— 那会直接冲过调用方设的断点。
// 只有"是我们中断下来的"才归我们放回。
// 这个用例同时是哨兵:若实现里误调了 Continue(),会打到 nil pty 上 panic。
func TestWhyOnDeliberateStopDoesNotResume(t *testing.T) {
	s := waitTestSession()
	s.started = true
	s.cur = StopInfo{
		Reason: "breakpoint",
		File:   "a.4gl",
		Line:   113,
		Source: []SourceLine{{Num: 113, Text: "  LET x = 1", IsCur: true}},
	}
	res, err := s.Why(true)
	if err != nil {
		t.Fatalf("停在停站态探测应成功(且不发任何命令): %v", err)
	}
	if res.WaitingForUser || res.Kind != "" {
		t.Fatalf("LET 行不该判成交互语句: %+v", res)
	}
	if res.Resumed {
		t.Fatal("程序仍停在断点上,不该报已放回")
	}
	if res.Risk != "" {
		t.Fatalf("没打断过就不该有风险提醒(会误导调用方): %q", res.Risk)
	}
}

// 停在交互语句上时要认出来 —— 这是"程序在等用户"这条判断的落点
func TestWhyDetectsInteractiveStop(t *testing.T) {
	s := waitTestSession()
	s.started = true
	s.cur = StopInfo{
		Reason: "interrupt",
		File:   "a.4gl",
		Line:   220,
		Source: []SourceLine{{Num: 220, Text: `  DISPLAY ARRAY contlist TO sr.*`, IsCur: true}},
	}
	res, err := s.Why(false) // 不自动放回,免得走 Continue()
	if err != nil {
		t.Fatal(err)
	}
	if !res.WaitingForUser {
		t.Fatalf("应判定为在等用户: %+v", res)
	}
	if res.Kind != "display_array" {
		t.Fatalf("类别应为 display_array,实际 %q", res.Kind)
	}
}

// ---------- 停站识别(放行类命令停下时没有位置头的情况) ----------

// 【核心修复】step/next 停下时 fgldb 可能**只打源码窗、不打任何位置头**
// (既没有 `Breakpoint N, ...` 也没有 `func() at file:line`)。
// 以前这种停站根本不算停站:collect 从不创建、onStop 从不被调用 ——
// 停站事件不发、s.cur 不更新,于是界面上代码画面完全不会跟随 AI 的步进。
// 这条直接喂协议行,验证整条链路。
func TestStepStopWithoutHeaderIsRecognized(t *testing.T) {
	s := waitTestSession()
	s.started = true
	evs := collectEvents(s)
	p := &pendingCmd{cmd: "next", mode: waitPrompt, res: make(chan *execResult, 1)}
	s.pending = p

	s.onLine("-> 812      DEFER INTERRUPT")
	s.onLine("   813      LET g_x = 1")
	s.onLine("(fgldb) ")

	select {
	case r := <-p.res:
		if r.Stop == nil {
			t.Fatal("停站应回填进命令响应")
		}
		if r.Stop.Line != 812 {
			t.Fatalf("停站行应为 812,实际 %d", r.Stop.Line)
		}
	default:
		t.Fatal("带箭头的源码行应被当成一次停站并收口命令")
	}
	if !hasEvent(*evs, "stopped") {
		t.Fatal("必须发出 stopped 事件 —— 否则界面上的代码画面不会跟随")
	}
	if got := s.Cur().Line; got != 812 {
		t.Fatalf("s.cur 应更新到 812,实际 %d", got)
	}
	if s.State() != StateStopped {
		t.Fatalf("停站后状态应为 stopped,实际 %s", s.State())
	}
}

// 非放行类命令带出的源码行**不算停站** —— 否则 `list` 之类会被误判。
func TestPlainSourceLineIsNotAStop(t *testing.T) {
	s := waitTestSession()
	evs := collectEvents(s)
	p := &pendingCmd{cmd: "list", mode: waitPrompt, res: make(chan *execResult, 1)}
	s.pending = p

	s.onLine("-> 812      DEFER INTERRUPT")
	s.onLine("(fgldb) ")

	if hasEvent(*evs, "stopped") {
		t.Fatal("非放行类命令的源码行不该被当成停站")
	}
	if got := s.Cur().Line; got != 0 {
		t.Fatalf("不该更新停站位置,实际 %d", got)
	}
}

// 无位置头的 step/next 只打源码窗,协议不给文件名 —— 必须沿用停站前的位置。
// 不补的后果很具体:停站文件的本地副本拿不到文件名(镜像落不下来),
// 前端也要多花一次 where 才知道自己在哪个文件里。
func TestHeadlessStepCarriesForwardFile(t *testing.T) {
	s := waitTestSession()
	collectEvents(s)
	s.cur = StopInfo{File: "asf_bsft001_wf.4gl", Line: 294, Func: "b_fill"}
	s.pending = &pendingCmd{cmd: "next", mode: waitPrompt, res: make(chan *execResult, 1)}

	s.onLine("-> 295         IF NOT lb_result THEN")
	s.onLine("  296            LET g_errno = \"x\"")
	s.onLine("(fgldb) ")

	if got := s.Cur().File; got != "asf_bsft001_wf.4gl" {
		t.Fatalf("同文件步进应沿用停站前的文件名,实际 %q", got)
	}
	if got := s.Cur().Line; got != 295 {
		t.Fatalf("行号应为 295,实际 %d", got)
	}
}

// 中断块同属"没有位置头",但它可能停在**另一个文件**上 —— 不能照搬当前文件,
// 仍要留给 where 去问。照搬的后果是 Why() 看到 File 非空就跳过 where,
// 拿着错文件去判断"是不是在等用户"。
func TestInterruptStopDoesNotGuessFile(t *testing.T) {
	s := waitTestSession()
	collectEvents(s)
	s.state = StateRunning
	s.cur = StopInfo{File: "asf_bsft001_wf.4gl", Line: 294}
	s.pending = &pendingCmd{cmd: "continue", mode: waitPrompt, res: make(chan *execResult, 1)}

	s.onLine("^C") // SIGINT 回显:这就是中断标记
	s.onLine("  812      DEFER INTERRUPT")
	s.onLine("-> 813         ...")
	s.onLine("(fgldb) ")

	if got := s.Cur().File; got != "" {
		t.Fatalf("中断停站不该猜文件名(该由 where 定),实际 %q", got)
	}
}

// 放行类命令一发出,状态必须如实翻 running。
// 假装还停在 stopped 的后果很实际:interrupt 会被自己的状态检查拒掉,
// 程序卡在对话框里时连中断都发不出去(真机验证时撞到过这个死局)。
func TestResumeCmdFlipsToRunning(t *testing.T) {
	if !IsResumeCmd("next") || !IsResumeCmd("continue") || !IsResumeCmd("s") {
		t.Fatal("next/continue/s 应判定为放行类命令")
	}
	if IsResumeCmd("print x") || IsResumeCmd("list") || IsResumeCmd("") {
		t.Fatal("print/list/空命令不该判定为放行类")
	}
}

// 断点类裸命令必须被识别出来,好让 /raw 回灌会话的断点缓存。
// 不识别的话,AI 用 exec "break X" 下的断点在快照里等于不存在:
// 前端断点列表空白、persistBPs 也存不下来(真机验证时撞到过)。
func TestIsBreakpointCmd(t *testing.T) {
	for _, c := range []string{"break 3936", "break MAIN", "BREAK 12 if x>1", "tbreak main",
		"clear 3", "delete 2", "disable 1", "enable 1"} {
		if !IsBreakpointCmd(c) {
			t.Errorf("%q 应判定为断点类命令", c)
		}
	}
	for _, c := range []string{"next", "print x", "info breakpoints", "list", "continue", ""} {
		if IsBreakpointCmd(c) {
			t.Errorf("%q 不该判定为断点类命令", c)
		}
	}
}

// ---------- wait 长轮询 ----------

func TestWaitMatch(t *testing.T) {
	want := map[string]bool{"stopped": true, "exit": true}
	if !waitMatch(Event{Type: "stopped"}, want) {
		t.Fatal("stopped 应命中")
	}
	// exit 在事件流里是 state 事件的取值,不是独立类型
	if !waitMatch(Event{Type: "state", State: string(StateExit)}, want) {
		t.Fatal("state=exit 应映射为 exit")
	}
	if waitMatch(Event{Type: "state", State: string(StateRunning)}, want) {
		t.Fatal("state=running 不该命中")
	}
	if waitMatch(Event{Type: "log"}, want) {
		t.Fatal("log 不该命中")
	}
}

// 入口停站只发 `state(stopped)`、**不发 `stopped` 事件**(见 startRun)。
// /wait --for stopped 必须认它 —— 这是真机验证时抓到的回归:
// tdebug start 改用 /wait 之后会一直等到超时,而会话其实早就停在入口了。
func TestWaitMatchRecognizesEntryStop(t *testing.T) {
	want := map[string]bool{"stopped": true, "exit": true}
	if !waitMatch(Event{Type: "state", State: string(StateStopped)}, want) {
		t.Fatal("state=stopped 必须命中 stopped(入口停站走的就是这条路)")
	}
	if waitMatch(Event{Type: "state", State: string(StateLoading)}, want) {
		t.Fatal("loading 不该命中")
	}
}

// 先订阅、后判现状:请求发出时已经停在停站态,必须立刻返回而不是等超时
func TestWaitEndpointImmediateWhenStopped(t *testing.T) {
	srv := NewServer(&Config{Listen: "127.0.0.1:0"}, nil, "")
	sess := waitTestSession()
	sess.cur = StopInfo{Reason: "breakpoint", File: "a.4gl", Line: 10}
	srv.mgr.sessions[sess.ID] = sess

	mux := http.NewServeMux()
	srv.routes(mux)
	rec := httptest.NewRecorder()
	start := time.Now()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/sessions/s1/wait?for=stopped&timeout=30", nil))
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("已停站就应立即返回,实际耗时 %v", elapsed)
	}
	if rec.Code != 200 {
		t.Fatalf("应返回 200,实际 %d", rec.Code)
	}
	var resp struct {
		OK       bool `json:"ok"`
		TimedOut bool `json:"timedOut"`
		Stop     *struct {
			File string `json:"file"`
		} `json:"stop"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.TimedOut {
		t.Fatalf("应命中停站而非超时: %s", rec.Body.String())
	}
	if resp.Stop == nil || resp.Stop.File != "a.4gl" {
		t.Fatalf("应带停站现场: %s", rec.Body.String())
	}
}

// 超时不是错误:200 + timedOut,而不是 4xx/5xx
func TestWaitEndpointTimeoutIsNotAnError(t *testing.T) {
	srv := NewServer(&Config{Listen: "127.0.0.1:0"}, nil, "")
	sess := waitTestSession()
	sess.state = StateRunning
	sess.runningSince = time.Now()
	srv.mgr.sessions[sess.ID] = sess

	mux := http.NewServeMux()
	srv.routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/sessions/s1/wait?for=stopped&timeout=1", nil))

	if rec.Code != 200 {
		t.Fatalf("超时应返回 200(不是错误),实际 %d", rec.Code)
	}
	var resp struct {
		TimedOut bool   `json:"timedOut"`
		State    string `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.TimedOut {
		t.Fatalf("应标记 timedOut: %s", rec.Body.String())
	}
	if resp.State != "running" {
		t.Fatalf("应回报当前状态 running,实际 %q", resp.State)
	}
}

// 长轮询订阅必须在请求结束时释放,否则 m.subs 会无限泄漏
func TestWaitEndpointReleasesSubscription(t *testing.T) {
	srv := NewServer(&Config{Listen: "127.0.0.1:0"}, nil, "")
	sess := waitTestSession()
	srv.mgr.sessions[sess.ID] = sess

	mux := http.NewServeMux()
	srv.routes(mux)

	before := len(srv.mgr.subs)
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/sessions/s1/wait?timeout=1", nil))
	after := len(srv.mgr.subs)
	if after != before {
		t.Fatalf("订阅未释放: 前 %d 后 %d", before, after)
	}
}
