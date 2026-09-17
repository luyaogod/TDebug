//go:build windows

package cli

import (
	"os/exec"
	"strings"
	"testing"
)

// 用临时注册表键验证 读取/写入/幂等/保留可展开变量,不触碰真实用户 PATH。
func TestUserPathInstallWindows(t *testing.T) {
	const keyPath = `Software\TDebugPathInstallTest`
	cleanup := func() { exec.Command("reg", "delete", `HKCU\`+keyPath, "/f").Run() }
	cleanup()
	defer cleanup()
	if out, err := exec.Command("reg", "add", `HKCU\`+keyPath, "/f").CombinedOutput(); err != nil {
		t.Fatalf("建临时注册表键失败: %v: %s", err, out)
	}

	old := userEnvKeyPath
	userEnvKeyPath = keyPath
	defer func() { userEnvKeyPath = old }()

	// 值不存在 → 原值为空、类型默认 REG_EXPAND_SZ
	cur, typ, err := userPath()
	if err != nil {
		t.Fatalf("userPath(空): %v", err)
	}
	if cur != "" || typ != "REG_EXPAND_SZ" {
		t.Fatalf(`空值应返回 ("", REG_EXPAND_SZ), got (%q, %q)`, cur, typ)
	}

	dir := `D:\tool\bin`
	next, added := mergeUserPath(cur, dir)
	if !added || next != dir {
		t.Fatalf("merge 空 PATH: added=%v next=%q", added, next)
	}
	if err := setUserPath(next, typ); err != nil {
		t.Fatalf("setUserPath: %v", err)
	}
	got, gotTyp, err := userPath()
	if err != nil {
		t.Fatalf("userPath(写入后): %v", err)
	}
	if got != dir {
		t.Fatalf("写入后读回 %q, want %q", got, dir)
	}
	if gotTyp != "REG_EXPAND_SZ" {
		t.Fatalf("类型应为 REG_EXPAND_SZ, got %q", gotTyp)
	}

	// 幂等:再次合并(含大小写与尾分隔符差异)不应变化
	if n2, added2 := mergeUserPath(got, `d:\TOOL\bin\`); added2 || n2 != got {
		t.Fatalf("重复合并应幂等: added=%v next=%q", added2, n2)
	}

	// 展开式变量必须原样保留(不能被展开后写回)
	withVar := `%USERPROFILE%\bin;` + dir
	next2, added2 := mergeUserPath(withVar, `D:\other`)
	if !added2 {
		t.Fatal("应追加新目录")
	}
	if err := setUserPath(next2, typ); err != nil {
		t.Fatalf("setUserPath(带变量): %v", err)
	}
	got2, _, err := userPath()
	if err != nil {
		t.Fatalf("userPath: %v", err)
	}
	if !strings.Contains(got2, `%USERPROFILE%\bin`) {
		t.Fatalf("展开式变量被改写了: %q", got2)
	}
	if !strings.HasSuffix(got2, `;D:\other`) {
		t.Fatalf("追加失败: %q", got2)
	}
}

// parseRegQueryPath 是纯函数:验证类型解析与折行拼接(长 PATH 实测会折行)。
func TestParseRegQueryPath(t *testing.T) {
	val, typ, err := parseRegQueryPath("\r\nHKEY_CURRENT_USER\\Environment\r\n    Path    REG_EXPAND_SZ    C:\\a;D:\\b\r\n")
	if err != nil {
		t.Fatalf("parseRegQueryPath: %v", err)
	}
	if val != `C:\a;D:\b` || typ != "REG_EXPAND_SZ" {
		t.Fatalf("got (%q, %q)", val, typ)
	}

	// 折行:续行原样拼回,不插空格
	val, _, err = parseRegQueryPath("HKEY_CURRENT_USER\\Environment\r\n    Path    REG_EXPAND_SZ    C:\\aaa;D:\\bb\r\nb;E:\\ccc\r\n")
	if err != nil {
		t.Fatalf("parseRegQueryPath(折行): %v", err)
	}
	if val != `C:\aaa;D:\bbb;E:\ccc` {
		t.Fatalf("折行拼接错误: %q", val)
	}
}
