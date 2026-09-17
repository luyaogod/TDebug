package debug

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// ---------- 模式与写闸门 ----------

// 模式决定"谁能写":
//
//	solo(纯人工):只有人能写,AI 只能读
//	collab(协作):只有 AI 能写,人只读
//
// 这条规则是整个方案的地基 —— 人和 AI 打同一批 REST 端点,服务端分辨不出身份,
// 只能靠 X-Actor 声明 + 这里这张真值表。
func TestModeMayWrite(t *testing.T) {
	cases := []struct {
		mode, actor string
		want        bool
	}{
		{ModeSolo, "human", true},
		{ModeSolo, "ai", false},      // 纯人工模式下 AI 只能读
		{ModeCollab, "human", false}, // 协作模式下人只读
		{ModeCollab, "ai", true},
		{"", "human", true}, // 空模式视为 solo(旧会话/未初始化)
		{"", "ai", false},
	}
	for _, c := range cases {
		s := waitTestSession()
		if c.mode != "" {
			s.SetMode(c.mode)
		}
		if got := s.MayWrite(c.actor); got != c.want {
			t.Errorf("mode=%q actor=%q MayWrite=%v,期望 %v", c.mode, c.actor, got, c.want)
		}
	}
}

func modeTestServer(t *testing.T, mode string) (*httptest.Server, *Server, *Session) {
	t.Helper()
	srv := NewServer(&Config{Listen: "127.0.0.1:0"}, nil, "")
	sess := waitTestSession()
	sess.SetMode(mode)
	srv.mgr.sessions[sess.ID] = sess
	mux := http.NewServeMux()
	srv.routes(mux)
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)
	return hs, srv, sess
}

func doReq(t *testing.T, method, url, actor string, body string) *http.Response {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if actor != "" {
		req.Header.Set(actorHeader, actor)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func respError(t *testing.T, resp *http.Response) string {
	t.Helper()
	var r struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&r)
	return r.Error
}

// 闸门在真实端点上生效,且拒绝文案要能直接照做(不是光说"没权限")
func TestWriteGateRejectsWithActionableMessage(t *testing.T) {
	// 协作模式下人不能写
	hs, _, _ := modeTestServer(t, ModeCollab)
	resp := doReq(t, "POST", hs.URL+"/api/sessions/s1/raw", "", `{"command":"print x"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("协作模式下人的写操作应 403,实际 %d", resp.StatusCode)
	}
	if msg := respError(t, resp); !strings.Contains(msg, "委托给 AI") || !strings.Contains(msg, "接管") {
		t.Fatalf("拒绝文案应指出怎么办(委托给 AI / 点接管),实际 %q", msg)
	}

	// 纯人工模式下 AI 不能写
	hs2, _, _ := modeTestServer(t, ModeSolo)
	resp2 := doReq(t, "POST", hs2.URL+"/api/sessions/s1/raw", "ai", `{"command":"print x"}`)
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("纯人工模式下 AI 的写操作应 403,实际 %d", resp2.StatusCode)
	}
	if msg := respError(t, resp2); !strings.Contains(msg, "交给 AI") {
		t.Fatalf("拒绝文案应指出让用户点「交给 AI」,实际 %q", msg)
	}
}

// 操作**成功之后**才归因:前端那条 origin:'ai' 渲染路径靠它亮起来。
// 用 /autovars 当载体:它只改会话内的一个开关,不碰 pty。
func TestWriteGateEmitsAIAction(t *testing.T) {
	hs, srv, sess := modeTestServer(t, ModeCollab)
	resp := doReq(t, "POST", hs.URL+"/api/sessions/"+sess.ID+"/autovars", "ai", `{"auto":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应放行,实际 %d: %s", resp.StatusCode, respError(t, resp))
	}
	found := false
	for _, ev := range srv.mgr.Events(0) {
		if ev.Type == "ai_action" && ev.Action == "session.autovars" && ev.Actor == "ai" {
			found = true
		}
	}
	if !found {
		t.Fatalf("放行且成功的 AI 写操作应产生 ai_action 事件,实际 %+v", srv.mgr.Events(0))
	}
}

// 失败的操作**不该**进时间线 —— 真机验证时,一次"上一条命令仍在执行"的忙等
// 把 60 多次根本没执行的 next 全记了进去。归因放在操作之后就是为了堵这个。
func TestFailedWriteDoesNotEmitAIAction(t *testing.T) {
	hs, srv, sess := modeTestServer(t, ModeCollab)
	// /calibrate 在没有停站现场时会直接返回错误,不会碰到 pty
	resp := doReq(t, "POST", hs.URL+"/api/sessions/"+sess.ID+"/calibrate", "ai", "")
	if resp.StatusCode == http.StatusOK {
		t.Fatal("这个载体应当失败(没有停站现场),测试前提不成立")
	}
	for _, ev := range srv.mgr.Events(0) {
		if ev.Type == "ai_action" {
			t.Fatalf("失败的操作不该进时间线: %+v", ev)
		}
	}
}

// 人不该因为只读而被记成 AI 的操作 —— 归因错了比不归因更坏
func TestHumanWriteDoesNotEmitAIAction(t *testing.T) {
	hs, srv, sess := modeTestServer(t, ModeSolo)
	doReq(t, "POST", hs.URL+"/api/sessions/"+sess.ID+"/calibrate", "", "")
	for _, ev := range srv.mgr.Events(0) {
		if ev.Type == "ai_action" {
			t.Fatalf("人的操作不该发 ai_action: %+v", ev)
		}
	}
}

// 人在协作模式下的只读操作必须照常放行 —— 否则"只读能力保留"就是空话
func TestReadOnlyAlwaysAllowed(t *testing.T) {
	hs, _, _ := modeTestServer(t, ModeCollab)
	resp := doReq(t, "GET", hs.URL+"/api/sessions/s1", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("协作模式下人的只读请求应放行,实际 %d", resp.StatusCode)
	}
	resp2 := doReq(t, "GET", hs.URL+"/api/sessions/s1", "ai", "")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("AI 的只读请求也应放行,实际 %d", resp2.StatusCode)
	}
}

// 已停站时的 /why 是纯只读分类,要放行给人;运行中会发 SIGINT,才受闸门约束
func TestWhyIsConditionalWrite(t *testing.T) {
	hs, _, sess := modeTestServer(t, ModeCollab)
	sess.started = true
	sess.cur = StopInfo{Reason: "breakpoint", File: "a.4gl", Line: 10,
		Source: []SourceLine{{Num: 10, Text: "  LET x = 1", IsCur: true}}}
	resp := doReq(t, "POST", hs.URL+"/api/sessions/s1/why", "", `{"resume":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("停站态下人的 why 应放行(只读分类),实际 %d: %s", resp.StatusCode, respError(t, resp))
	}
}

// 模式切换的权限是不对称的:人可任意方向切;AI 不能自行解除纯人工模式
func TestModeSwitchRules(t *testing.T) {
	hs, _, sess := modeTestServer(t, ModeSolo)

	// AI 想把 solo 切成 collab → 拒绝
	resp := doReq(t, "POST", hs.URL+"/api/sessions/"+sess.ID+"/mode", "ai", `{"mode":"collab"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("AI 不能自升权限,应 403,实际 %d", resp.StatusCode)
	}
	if sess.Mode() != ModeSolo {
		t.Fatal("被拒的切换不该改变模式")
	}

	// 人切到 collab → 放行
	resp2 := doReq(t, "POST", hs.URL+"/api/sessions/"+sess.ID+"/mode", "", `{"mode":"collab"}`)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("人应可任意方向切换,实际 %d", resp2.StatusCode)
	}
	if sess.Mode() != ModeCollab {
		t.Fatalf("模式应已切换,实际 %q", sess.Mode())
	}

	// AI 把 collab 降回 solo(让出控制权) → 放行
	resp3 := doReq(t, "POST", hs.URL+"/api/sessions/"+sess.ID+"/mode", "ai", `{"mode":"solo"}`)
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("AI 让出控制权应放行,实际 %d", resp3.StatusCode)
	}
	if sess.Mode() != ModeSolo {
		t.Fatalf("模式应回到 solo,实际 %q", sess.Mode())
	}

	// 非法取值
	resp4 := doReq(t, "POST", hs.URL+"/api/sessions/"+sess.ID+"/mode", "", `{"mode":"whatever"}`)
	if resp4.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法模式应 400,实际 %d", resp4.StatusCode)
	}
}

// ---------- 事件序号与开场补发 ----------

// Seq 必须在并发灌入下严格递增且不重复 —— WS 补发与前端去重全靠它
func TestEmitAssignsUniqueIncreasingSeq(t *testing.T) {
	m := NewManager(&Config{})
	const n, goroutines = 50, 8

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				m.emit(Event{Type: "log", Text: "x"})
			}
		}()
	}
	wg.Wait()

	evs := m.Events(0)
	if len(evs) != n*goroutines {
		t.Fatalf("事件数应为 %d,实际 %d", n*goroutines, len(evs))
	}
	seen := map[uint64]bool{}
	for _, ev := range evs {
		if ev.Seq == 0 {
			t.Fatal("每条事件都应有非零序号")
		}
		if seen[ev.Seq] {
			t.Fatalf("序号重复: %d", ev.Seq)
		}
		seen[ev.Seq] = true
	}
}

// 订阅带补发:不遗漏是硬要求(补发丢一条,时间线就少一段历史)。
// 序列号重复是允许的(emit 的 append 与 broadcast 之间不持锁),由 Seq 去重兜住。
func TestSubscribeWithReplayNoLoss(t *testing.T) {
	m := NewManager(&Config{})
	const total = 200

	for i := 0; i < total/2; i++ {
		m.emit(Event{Type: "log", Text: "before"})
	}
	ch, replay, cancel := m.SubscribeWithReplay("t", 1000)
	defer cancel()
	for i := 0; i < total/2; i++ {
		m.emit(Event{Type: "log", Text: "after"})
	}

	seen := map[uint64]bool{}
	for _, ev := range replay {
		seen[ev.Seq] = true
	}
	deadline := time.After(3 * time.Second)
	for len(seen) < total {
		select {
		case ev := <-ch:
			seen[ev.Seq] = true
		case <-deadline:
			t.Fatalf("漏事件:只覆盖 %d/%d 条", len(seen), total)
		}
	}
}

// 建连第一帧必须是 replay(页面刷新后时间线不该是空的),第二帧是 hello 哨兵
func TestWSReplaysHistoryOnConnect(t *testing.T) {
	srv := NewServer(&Config{Listen: "127.0.0.1:0"}, nil, "")
	srv.mgr.emit(Event{Type: "log", Text: "历史一"})
	srv.mgr.emit(Event{Type: "ai_action", Action: "bp.add", Text: "历史二"})
	srv.mgr.emit(Event{Type: "state", State: "stopped"}) // 状态类不进补发批次

	mux := http.NewServeMux()
	srv.routes(mux)
	hs := httptest.NewServer(mux)
	defer hs.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http")+"/api/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var frame struct {
		Type   string  `json:"type"`
		Epoch  string  `json:"epoch"`
		Events []Event `json:"events"`
	}
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != "replay" {
		t.Fatalf("建连第一帧必须是 replay,实际 %q", frame.Type)
	}
	if frame.Epoch == "" {
		t.Fatal("replay 帧应带 epoch(前端据此判断服务端是否重启过)")
	}
	counts := map[string]int{}
	for _, ev := range frame.Events {
		counts[ev.Type]++
	}
	if counts["log"] != 1 || counts["ai_action"] != 1 {
		t.Fatalf("补发内容不对: %v", counts)
	}
	if counts["state"] != 0 {
		t.Fatalf("状态类事件不该进补发批次(会拿旧状态盖新状态): %v", counts)
	}

	_, data2, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var hello struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(data2, &hello)
	if hello.Type != "hello" {
		t.Fatalf("第二帧应为 hello,实际 %q", hello.Type)
	}
}

// 快照要暴露 mode 与 inflight —— 界面上"AI 正在执行 continue(已 12s)"全靠它
func TestSnapshotExposesModeAndInflight(t *testing.T) {
	srv := NewServer(&Config{Listen: "127.0.0.1:0"}, nil, "")
	sess := waitTestSession()
	sess.SetMode(ModeCollab)
	srv.mgr.sessions[sess.ID] = sess
	sess.mu.Lock()
	sess.pending = &pendingCmd{cmd: "continue", res: make(chan *execResult, 1), startedAt: time.Now().Add(-12 * time.Second)}
	sess.mu.Unlock()

	brief := srv.mgr.Snapshot()
	if len(brief) != 1 {
		t.Fatalf("应有 1 个会话,实际 %d", len(brief))
	}
	if brief[0].Mode != ModeCollab {
		t.Fatalf("快照应带 mode,实际 %q", brief[0].Mode)
	}
	if brief[0].Inflight == nil {
		t.Fatal("快照应带 inflight(在飞命令)")
	}
	if brief[0].Inflight.Cmd != "continue" {
		t.Fatalf("inflight 命令名不对: %q", brief[0].Inflight.Cmd)
	}
	if brief[0].Inflight.Elapsed < 11 || brief[0].Inflight.Elapsed > 14 {
		t.Fatalf("inflight 已执行时长应约 12s,实际 %.1f", brief[0].Inflight.Elapsed)
	}

	sess.mu.Lock()
	sess.pending = nil
	sess.mu.Unlock()
	if got := srv.mgr.Snapshot()[0].Inflight; got != nil {
		t.Fatalf("空闲时不该有 inflight,实际 %+v", got)
	}
}

// 小工具:直接取一次环形缓冲里的全部事件(按会话 id 过滤)

// 空会话不该把 AI 关在门外。
//
// 前端一连上来就会建一个还没挂程序的宿主会话,而它是 human 身份建的(→ 纯人工)。
// 若把它也算作"要保护的一轮",AI 就再也启动不了任何调试 —— 连自己发起都做不到
// (真机上撞到过:一个 module/prog 全空、什么都没在跑的会话,把 wsdebug 挡在 403)。
func TestEmptySessionDoesNotBlockLaunch(t *testing.T) {
	for _, mode := range []string{ModeSolo, ModeCollab} {
		srv := NewServer(&Config{Listen: "127.0.0.1:0"}, nil, "")
		sess := waitTestSession() // waitTestSession 就是空会话:module/prog 都没挂
		sess.SetMode(mode)
		srv.mgr.sessions[sess.ID] = sess
		if !sess.Bare() {
			t.Fatal("测试前提:waitTestSession 应当是个没挂程序的空会话")
		}

		ask := func() (bool, *httptest.ResponseRecorder) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/sessions", nil)
			req.Header.Set(actorHeader, "ai")
			return srv.sessOfWriteGlobal(rec, req), rec
		}

		// 空会话:AI 一律放行 —— 没有程序,就没有可被抢走的一轮运行
		if ok, rec := ask(); !ok {
			t.Fatalf("%s 下的空会话不该拦住 AI 启动调试:HTTP %d %s", mode, rec.Code, rec.Body.String())
		}

		// 一旦挂上程序,闸门立刻恢复效力
		sess.Prog, sess.Module = "bsft001_wf", "asf"
		ok, rec := ask()
		if mode == ModeSolo && ok {
			t.Fatal("有程序在跑时,纯人工模式必须拦住 AI 的启动")
		}
		if mode == ModeCollab && !ok {
			t.Fatalf("协作模式下 AI 应能启动,却被拒:%d %s", rec.Code, rec.Body.String())
		}
	}
}
