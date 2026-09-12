//go:build windows

package cli

// Windows 后台常驻:以 DETACHED_PROCESS 方式脱离当前控制台启动子进程,
// 输出重定向到日志文件;父进程退出后子进程继续运行。

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
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
	// DETACHED_PROCESS(0x8) 脱离控制台;CREATE_NEW_PROCESS_GROUP(0x200) 独立进程组
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x8 | 0x200}
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
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// killProcess 结束指定进程(后台实例)。
func killProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
