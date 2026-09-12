//go:build !windows

package cli

// 非 Windows 后台常驻:以 Setsid 方式脱离会话启动子进程,输出重定向到日志文件。

import (
	"os"
	"os/exec"
	"syscall"
)

// spawnDetached 启动后台子进程(不等待),返回其 pid。
func spawnDetached(exe string, args []string, logPath string) (int, error) {
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer logf.Close()

	cmd := exec.Command(exe, args...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), "TDEBUG_SERVE_LOG="+logPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	go cmd.Wait() // 回收句柄,不阻塞
	return cmd.Process.Pid, nil
}

// pidAlive 判断 pid 是否存活。
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err == nil {
		return true
	}
	return false
}

// killProcess 结束指定进程(后台实例)。
func killProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
