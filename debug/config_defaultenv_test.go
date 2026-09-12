package debug

import (
	"strings"
	"testing"

	"tdebug/host"
)

// ApplyDefaultEnv 在 sshs 被删空时不得 panic。
// 回归场景(hSettingsPut):设置页删光所有环境后保存,请求体 sshs 为空,
// 但服务端会把运行时 envName(非空)代入,修复前回落分支对空切片取下标越界。
func TestApplyDefaultEnvEmptySSHs(t *testing.T) {
	c := &Config{envName: "主机正式区", SSH: host.SSHConfig{Host: "10.0.0.2"}, Zone: "36"}
	c.ApplyDefaultEnv()
	if c.envName != "" {
		t.Fatalf("sshs 为空应清空当前环境,got %q", c.envName)
	}
	if c.SSH.Host != "" || c.Zone != "" {
		t.Fatalf("sshs 为空运行时字段应清空,got host=%q zone=%q", c.SSH.Host, c.Zone)
	}
	// EnvName() 会用 SSH.Host+Zone 兜底;残留会拼出幽灵环境名 "10.0.0.2-36"
	if got := c.EnvName(); strings.Contains(got, "10.0.0.2") {
		t.Fatalf("sshs 为空不应拼出幽灵环境名,got %q", got)
	}
}

// 当前环境已被删除但仍有其它环境时,回落到首条(原有语义不变)。
func TestApplyDefaultEnvFallsBackToFirst(t *testing.T) {
	c := &Config{
		SSHs: []host.NamedSsh{
			{Name: "A", SSHConfig: host.SSHConfig{Host: "1.1.1.1"}},
			{Name: "B", SSHConfig: host.SSHConfig{Host: "2.2.2.2"}},
		},
		envName: "已删除的环境",
	}
	c.ApplyDefaultEnv()
	if c.envName != "A" {
		t.Fatalf("应回落到首条环境 A,got %q", c.envName)
	}
	if c.SSH.Host != "1.1.1.1" {
		t.Fatalf("回落应合并首条 SSH,got %q", c.SSH.Host)
	}
}

// envName 命中时应合并该环境的连接(原有语义不变)。
func TestApplyDefaultEnvMatchesByName(t *testing.T) {
	c := &Config{
		SSHs: []host.NamedSsh{
			{Name: "A", SSHConfig: host.SSHConfig{Host: "1.1.1.1"}},
			{Name: "B", SSHConfig: host.SSHConfig{Host: "2.2.2.2", Port: 2222}},
		},
		envName: "B",
	}
	c.ApplyDefaultEnv()
	if c.envName != "B" || c.SSH.Host != "2.2.2.2" || c.SSH.Port != 2222 {
		t.Fatalf("应按名合并环境 B,got %q %q:%d", c.envName, c.SSH.Host, c.SSH.Port)
	}
}
