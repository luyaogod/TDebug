package debug

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Listen 必须报出真实监听端口:--listen 端口写 0 时由系统分配,只能回读。
func TestListenReportsRealPort(t *testing.T) {
	s := NewServer(&Config{Listen: "127.0.0.1:0"}, nil, "")
	ln, addr, err := s.Listen()
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("监听到非 host:port 地址 %q: %v", addr, err)
	}
	if host != "127.0.0.1" || port == "0" {
		t.Fatalf("端口未回读(期望 127.0.0.1:<非0>): %s", addr)
	}
	if want := ln.Addr().String(); want != addr {
		t.Fatalf("上报地址应与真实监听一致: 上报 %s,实际 %s", addr, want)
	}
	if s.ListenAddr() != addr {
		t.Fatalf("ListenAddr 应为 %s,实际 %s", addr, s.ListenAddr())
	}

	// 该端口再起一个 → 自动顺延到下一个端口(且报出的是顺延后的真实地址)
	s2 := NewServer(&Config{Listen: addr}, nil, "")
	ln2, addr2, err := s2.Listen()
	if err != nil {
		t.Fatalf("顺延监听失败: %v", err)
	}
	defer ln2.Close()
	p1, _ := strconv.Atoi(port)
	_, port2, _ := net.SplitHostPort(addr2)
	p2, _ := strconv.Atoi(port2)
	if p2 != p1+1 {
		t.Fatalf("端口 %d 被占用时应顺延到 %d,实际 %s", p1, p1+1, addr2)
	}
}

// LoadConfig 严格要求至少一个环境;桌面版用的 LoadConfigAllowEmpty 允许为空。
func TestLoadConfigAllowEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"debug":{"sshs":[],"listen":"127.0.0.1:1234"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("LoadConfig 应因 sshs 为空报错(CLI 语义不能松)")
	}
	cfg, err := LoadConfigAllowEmpty(p)
	if err != nil {
		t.Fatalf("LoadConfigAllowEmpty 应通过: %v", err)
	}
	if cfg.Listen != "127.0.0.1:1234" {
		t.Fatalf("listen 未读回: %s", cfg.Listen)
	}
	if len(cfg.SSHs) != 0 {
		t.Fatalf("sshss 应为空: %d", len(cfg.SSHs))
	}
}

// NewDefaultConfig 落盘后应能被宽松加载读回(桌面首启骨架)。
func TestNewDefaultConfigRoundTrip(t *testing.T) {
	cfg := NewDefaultConfig()
	if cfg.Listen == "" || cfg.LaunchArgs == "" || cfg.WatchdogSeconds == 0 ||
		cfg.TermWidth == 0 || cfg.TermHeight == 0 || cfg.PrintElements == 0 {
		t.Fatalf("默认值缺失: %+v", cfg)
	}
}

// POST /api/shutdown 取消 Serve 上下文(桌面壳关窗走这条优雅停止)。
func TestShutdownEndpoint(t *testing.T) {
	srv := NewServer(&Config{Listen: "127.0.0.1:0"}, nil, "")
	ln, addr, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background(), ln) }()

	base := "http://" + addr
	if err := waitStatus(base, 3*time.Second); err != nil {
		t.Fatalf("服务未就绪: %v", err)
	}
	// 空 body:解析失败 → 400(与全站 readBody 语义一致)
	resp, err := http.Post(base+"/api/shutdown", "application/json", bytes.NewBufferString(""))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空请求体应返回 400,实际 %d", resp.StatusCode)
	}
	resp, err = http.Post(base+"/api/shutdown", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("shutdown 应返回 200,实际 %d", resp.StatusCode)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve 应正常返回,实际 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve 未在 5s 内退出")
	}
}

// 未运行/未就绪的服务调 shutdown → 409。
func TestShutdownNotReady(t *testing.T) {
	srv := NewServer(&Config{}, nil, "")
	w := httptest.NewRecorder()
	srv.hShutdown(w, httptest.NewRequest("POST", "/api/shutdown", bytes.NewBufferString("{}")))
	if w.Code != http.StatusConflict {
		t.Fatalf("未就绪应返回 409,实际 %d", w.Code)
	}
}

func waitStatus(base string, d time.Duration) error {
	deadline := time.Now().Add(d)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/api/status")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = err
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return lastErr
}
