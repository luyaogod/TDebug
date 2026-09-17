//go:build windows

package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// userEnvKeyPath HKCU 下的用户环境变量键;测试可覆盖为临时键,避免动真实 PATH。
var userEnvKeyPath = "Environment"

// userPath 读 HKCU\Environment\Path 的**原值**与注册表类型。
//
// 用 reg.exe 而不是 setx:
//   - setx 会把 `%USERPROFILE%` 这类展开式变量**展开**后写回,破坏原值;
//   - setx 还有 1024 字符截断的老问题。
//
// 值不存在时返回 ("", "REG_EXPAND_SZ", nil):新建即从空开始。
func userPath() (string, string, error) {
	out, err := exec.Command("reg", "query", `HKCU\`+userEnvKeyPath, "/v", "Path").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return "", "REG_EXPAND_SZ", nil // 值不存在
		}
		return "", "", fmt.Errorf("读用户环境变量失败(reg query HKCU\\%s /v Path): %w", userEnvKeyPath, err)
	}
	return parseRegQueryPath(string(out))
}

// parseRegQueryPath 解析 `reg query HKCU\Environment /v Path` 的输出。
//
// 输出形如(前面还有一行 HKEY_CURRENT_USER\Environment):
//
//	Path    REG_EXPAND_SZ    C:\a;D:\b
//
// 长值会按控制台宽度**折行**,续行必须原样拼回;折行处不插空格,所以直接拼接即可。
func parseRegQueryPath(out string) (string, string, error) {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for i, ln := range lines {
		j := strings.Index(ln, "REG_")
		if j < 0 {
			continue
		}
		rest := ln[j:]
		k := strings.IndexAny(rest, " \t")
		if k < 0 {
			return "", strings.TrimSpace(rest), nil // 类型后没有值 → 空 PATH
		}
		typ := rest[:k]
		val := strings.TrimLeft(rest[k:], " \t")
		for _, cont := range lines[i+1:] {
			val += cont
		}
		return strings.TrimRight(val, "\r\n"), typ, nil
	}
	return "", "", fmt.Errorf("reg query 的输出里找不到 Path 值(输出: %q)", strings.TrimSpace(out))
}

// setUserPath 写回用户 PATH,保持原有注册表类型(只有 REG_SZ 才用 REG_SZ)。
func setUserPath(val, typ string) error {
	t := "REG_EXPAND_SZ"
	if strings.EqualFold(strings.TrimSpace(typ), "REG_SZ") {
		t = "REG_SZ"
	}
	out, err := exec.Command("reg", "add", `HKCU\`+userEnvKeyPath, "/v", "Path", "/t", t, "/d", val, "/f").CombinedOutput()
	if err != nil {
		return fmt.Errorf("写用户 PATH 失败(reg add HKCU\\%s /v Path): %v: %s", userEnvKeyPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// broadcastEnvChange 广播 WM_SETTINGCHANGE("Environment"),让资源管理器刷新环境块。
//
// best-effort:注册表已经写好了,广播失败只是「已开的终端/资源管理器要重开」。
func broadcastEnvChange() error {
	const (
		hwndBroadcast   = 0xFFFF
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
	)
	proc := syscall.NewLazyDLL("user32.dll").NewProc("SendMessageTimeoutW")
	env, err := syscall.UTF16PtrFromString("Environment")
	if err != nil {
		return err
	}
	var res uintptr
	r, _, errno := proc.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)),
		smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&res)))
	if r == 0 {
		return fmt.Errorf("SendMessageTimeoutW 返回 0: %v", errno)
	}
	return nil
}
