package debug

import (
	"testing"

	"tdebug/dbconfig"
	"tdebug/host"
)

// 单一常驻会话:目标身份判定/空闲宿主复用/环境克隆的纯逻辑单测
// (SSH 登录等依赖真实 T100,不在此覆盖)

func cfgFor(h, zone string) *Config {
	c := &Config{}
	c.SSH = host.SSHConfig{Host: h, Port: 22, User: "u"}
	c.Zone = zone
	c.fillDefaults()
	return c
}

func fakeIdleSession(id, host, zone string) *Session {
	return &Session{
		ID:       id,
		cfg:      cfgFor(host, zone),
		state:    StateIdle,
		Module:   "oldm",
		Prog:     "oldp",
		bps:      map[int]*Breakpoint{},
		srcCache: map[string]srcCacheEntry{},
	}
}

func TestSessionSameTarget(t *testing.T) {
	s := fakeIdleSession("s1", "h1", "36")
	if !s.sameTarget(cfgFor("h1", "36")) {
		t.Fatal("同 host/zone 应判定为同一目标")
	}
	if s.sameTarget(cfgFor("h2", "36")) {
		t.Fatal("不同 host 不应判定为同一目标")
	}
	if s.sameTarget(cfgFor("h1", "35")) {
		t.Fatal("不同 zone 不应判定为同一目标")
	}
	if s.sameTarget(nil) {
		t.Fatal("nil 配置不应判定为同一目标")
	}
}

func TestPrepareSessionReusesIdleHost(t *testing.T) {
	m := NewManager(cfgFor("h1", "36"))
	live := fakeIdleSession("s1", "h1", "36")
	m.mu.Lock()
	m.sessions["s1"] = live
	m.mu.Unlock()

	sess, err := m.prepareSession(cfgFor("h1", "36"), "m1", "p1", "", "", "", "")
	if err != nil {
		t.Fatalf("prepareSession: %v", err)
	}
	if sess != live {
		t.Fatal("空闲同目标会话应被复用,而不是新建")
	}
	if sess.Module != "m1" || sess.Prog != "p1" {
		t.Fatalf("复用时应刷新本轮运行参数: module=%q prog=%q", sess.Module, sess.Prog)
	}
	if m.Get("s1") == nil {
		t.Fatal("复用后会话应仍登记在管理器")
	}
}

func TestPrepareSessionRejectsActiveRun(t *testing.T) {
	m := NewManager(cfgFor("h1", "36"))
	active := fakeIdleSession("s1", "h1", "36")
	active.mu.Lock()
	active.state = StateStopped
	active.mu.Unlock()
	m.mu.Lock()
	m.sessions["s1"] = active
	m.mu.Unlock()

	if _, err := m.prepareSession(cfgFor("h1", "36"), "m1", "p1", "", "", "", ""); err == nil {
		t.Fatal("同目标活跃会话(stopped)应报错,要求先结束当前调试")
	}
}

func TestTopentForRun(t *testing.T) {
	c := cfgFor("h1", "36")
	c.Topent = "7"
	s := fakeIdleSession("s1", "h1", "36")
	s.cfg = c
	if got := s.topentForRun(); got != "7" {
		t.Fatalf("无手动设置时应回退 ssh 环境 topent,got %q", got)
	}
	// 文本型 TOPENT(设置页/会话面板均允许)原样透传
	c2 := cfgFor("h1", "36")
	c2.Topent = " txt-9 "
	s3 := fakeIdleSession("s3", "h1", "36")
	s3.cfg = c2
	if got := s3.topentForRun(); got != "txt-9" {
		t.Fatalf("文本型配置 TOPENT 应剔除两侧空白透传,got %q", got)
	}
	s.mu.Lock()
	s.topentOverride = "99"
	s.mu.Unlock()
	if got := s.topentForRun(); got != "99" {
		t.Fatalf("手动设置应优先于配置企业,got %q", got)
	}
	s2 := fakeIdleSession("s2", "h1", "36")
	if got := s2.topentForRun(); got != "" {
		t.Fatalf("无 topent 且无手动设置应返回空(沿用登录默认),got %q", got)
	}
	if got := s.TopentOverride(); got != "99" {
		t.Fatalf("TopentOverride 应返回手动值,got %q", got)
	}
	if got := s.TopentCfg(); got != "7" {
		t.Fatalf("TopentCfg 应返回配置值,got %q", got)
	}
	// TopentShell 取登录回读的 $TOPENT:与配置级 TopentCfg 是两个来源
	// (连接会话不 export TOPENT,故"当前连的是哪个"以回读值为准)
	if got := s.TopentShell(); got != "" {
		t.Fatalf("未回读(无 Runtime)时 TopentShell 应为空,got %q", got)
	}
	c.Runtime = &host.RuntimeEnv{TOP: "/u1/t35prd", ERP: "/u1/t35prd/erp", Topent: " 13 "}
	if got := s.TopentShell(); got != "13" {
		t.Fatalf("TopentShell 应返回登录回读值并剔除空白,got %q", got)
	}
}

func TestCloneEnvAndEnvName(t *testing.T) {
	e1 := host.NamedSsh{Name: "E1", Zone: "35", Topent: "7"}
	e1.Host = "e1h"
	e1.Port = 22
	e1.User = "u1"
	e1.DB = &dbconfig.Connection{Type: "oracle", Host: "db1h", Port: 1521, Service: "s1"}
	e2 := host.NamedSsh{Name: "E2", Zone: "36"}
	e2.Host = "e2h"
	e2.Port = 22
	e2.User = "u2"
	c := &Config{
		SSH:  host.SSHConfig{Host: "top", Port: 22, User: "u"},
		Zone: "36",
		SSHs: []host.NamedSsh{e1, e2},
	}
	c.fillDefaults()

	clone := c.CloneEnv("E1")
	if clone == nil {
		t.Fatal("CloneEnv 应命中 E1")
	}
	if clone.SSH.Host != "e1h" || clone.Zone != "35" || clone.EnvName() != "E1" {
		t.Fatalf("CloneEnv 字段不符: %+v", clone.SSH)
	}
	if clone.Topent != "7" {
		t.Fatalf("CloneEnv 应带环境 TOPENT,got %q", clone.Topent)
	}
	if clone.DB == nil || clone.DB.Type != "oracle" || clone.DB.Service != "s1" {
		t.Fatal("CloneEnv 应带环境内嵌的 db 配置")
	}
	if c.SSH.Host != "top" {
		t.Fatal("CloneEnv 不应改动原配置")
	}
	if c.CloneEnv("Nope") != nil {
		t.Fatal("未命中环境应返回 nil")
	}
	clone2 := c.CloneEnv("E2")
	if clone2 == nil || clone2.DB != nil {
		t.Fatal("未挂 db 的环境应得到 nil DB")
	}
	if got := cfgFor("x", "36").EnvName(); got != "x-36" {
		t.Fatalf("未选定环境时环境名应为 host-zone,got %q", got)
	}
}
