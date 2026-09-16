package cli

import (
	"os"
	"path/filepath"
	"testing"

	"tdebug/debug"
)

// 桌面首启骨架:默认值齐全、listen 可覆盖、能被宽松加载读回,而严格加载仍拒绝空环境。
func TestWriteDesktopDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := writeDesktopDefaultConfig(p, ""); err != nil {
		t.Fatalf("写默认配置失败: %v", err)
	}
	cfg, err := debug.LoadConfigAllowEmpty(p)
	if err != nil {
		t.Fatalf("默认配置应可被宽松加载: %v", err)
	}
	if cfg.Listen != desktopDefaultListen {
		t.Fatalf("默认监听应为 %s,实际 %s", desktopDefaultListen, cfg.Listen)
	}
	if len(cfg.SSHs) != 0 {
		t.Fatalf("默认配置不应含环境: %d", len(cfg.SSHs))
	}
	if cfg.LaunchArgs == "" || cfg.WatchdogSeconds == 0 || cfg.PrintElements == 0 || cfg.TermWidth == 0 {
		t.Fatalf("默认值未落盘: %+v", cfg)
	}
	if _, err := debug.LoadConfig(p); err == nil {
		t.Fatal("LoadConfig 应仍拒绝空 sshs(CLI 语义不变)")
	}

	// --listen 覆盖:首启就把该值写进配置,后续启动端口稳定
	p2 := filepath.Join(dir, "c2.json")
	if err := writeDesktopDefaultConfig(p2, "127.0.0.1:9999"); err != nil {
		t.Fatal(err)
	}
	cfg2, err := debug.LoadConfigAllowEmpty(p2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Listen != "127.0.0.1:9999" {
		t.Fatalf("listen 覆盖未生效: %s", cfg2.Listen)
	}
}

// 桌面模式允许配置文件不存在(首启由命令自己建),不像 CLI 那样必须已存在。
func TestResolveDesktopConfigPathAllowsMissing(t *testing.T) {
	oldPath := configPath
	oldEnv, hadEnv := os.LookupEnv("TDEBUG_CONFIG")
	defer func() {
		configPath = oldPath
		if hadEnv {
			_ = os.Setenv("TDEBUG_CONFIG", oldEnv)
		} else {
			_ = os.Unsetenv("TDEBUG_CONFIG")
		}
	}()
	_ = os.Unsetenv("TDEBUG_CONFIG")

	target := filepath.Join(t.TempDir(), "sub", "config.json")
	configPath = target
	got, err := resolveDesktopConfigPath()
	if err != nil {
		t.Fatalf("解析路径不应报错: %v", err)
	}
	if got != target {
		t.Fatalf("期望 %s,实际 %s", target, got)
	}

	// TDEBUG_CONFIG 优先
	envPath := filepath.Join(t.TempDir(), "env.json")
	_ = os.Setenv("TDEBUG_CONFIG", envPath)
	got, err = resolveDesktopConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != envPath {
		t.Fatalf("TDEBUG_CONFIG 应优先: 期望 %s,实际 %s", envPath, got)
	}
}
