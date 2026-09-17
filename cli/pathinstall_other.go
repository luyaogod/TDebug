//go:build !windows

package cli

import "errors"

// 非 Windows 平台:`tdebug install path` 没有 HKCU 可写。
// 保留同样签名让 CLI 层跨平台可编译。

func userPath() (string, string, error) {
	return "", "", errors.New("tdebug install path 仅支持 Windows")
}

func setUserPath(string, string) error {
	return errors.New("tdebug install path 仅支持 Windows")
}

func broadcastEnvChange() error { return nil }
