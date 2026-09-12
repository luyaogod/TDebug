package cli

// debug serve 后台常驻支持:
//   - 默认 tdebug serve 在后台启动 HTTP 服务(单实例),打印实际地址后立即返回,
//     终端/会话不被占用,可继续输入其它命令;
//   - tdebug serve --foreground 前台运行(调试用,日志直接到终端);
//   - tdebug serve --stop 停止后台实例;
//   - 运行状态记录在 config.json 同目录的 .tdebug-serve.json(pid/url/日志),
//     端口被占用自动顺延时,debugctl 命令据状态文件自动找到真实地址。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"tdebug/debug"
)

const (
	serveStateName = ".tdebug-serve.json"
	serveLogName   = ".tdebug-serve.log"
)

var (
	dbgServeForeground bool
	dbgServeStop       bool
)

// serveInfo 后台实例状态:pid/url/日志,供单实例判断与客户端自动寻址。
type serveInfo struct {
	PID   int    `json:"pid"`
	URL   string `json:"url"`
	Log   string `json:"log"`
	Since string `json:"since"`
}

// serveInfoFile 返回状态文件路径(dir = config.json 所在目录)。
func serveInfoFile(dir string) string { return filepath.Join(dir, serveStateName) }

func writeServeInfo(st *serveInfo, dir string) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(serveInfoFile(dir), b, 0o644)
}

func readServeInfo(dir string) (*serveInfo, error) {
	b, err := os.ReadFile(serveInfoFile(dir))
	if err != nil {
		return nil, err
	}
	var st serveInfo
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	if st.PID <= 0 || st.URL == "" {
		return nil, fmt.Errorf("状态文件无效: %s", serveInfoFile(dir))
	}
	return &st, nil
}

func removeServeInfo(dir string) { _ = os.Remove(serveInfoFile(dir)) }

// serveUp 探活:GET /api/status 返回 200 且应答体带 tdebug-debug 标记才算运行中,
// 避免把占用同端口的其它 HTTP 服务误判为本实例。
func serveUp(url string) bool {
	if url == "" {
		return false
	}
	cli := &http.Client{Timeout: 2 * time.Second}
	resp, err := cli.Get(strings.TrimRight(url, "/") + "/api/status")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	return strings.Contains(string(buf[:n]), "tdebug-debug")
}

// runningInstance 返回本 config 目录下正在运行的后台实例;无则返回 nil。
func runningInstance(dir string) *serveInfo {
	st, err := readServeInfo(dir)
	if err != nil {
		return nil
	}
	if !serveUp(st.URL) {
		return nil
	}
	return st
}

// configDir 定位 config.json 所在目录(状态文件/日志存放处)。
func configDir() string {
	if p, err := resolveConfigPath(configPath); err == nil {
		return filepath.Dir(p)
	}
	abs, _ := filepath.Abs(configPath)
	return filepath.Dir(abs)
}

// configDebugListen 读 config.json debug.listen(尽力而为,失败返回空)。
func configDebugListen() string {
	p, err := resolveConfigPath(configPath)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var root struct {
		Debug struct {
			Listen string `json:"listen"`
		} `json:"debug"`
	}
	if json.Unmarshal(b, &root) != nil {
		return ""
	}
	return strings.TrimSpace(root.Debug.Listen)
}

// debugAutoURL 客户端自动寻址:状态文件实际地址 > config debug.listen > 内置默认。
func debugAutoURL() string {
	if st, err := readServeInfo(configDir()); err == nil && st.URL != "" {
		return st.URL
	}
	if u := configDebugListen(); u != "" {
		return "http://" + u
	}
	return "http://" + debug.DefaultListen
}

// logTail 返回日志文件末尾内容(后台启动失败时给出线索)。
func logTail(logPath string, max int) string {
	f, err := os.Open(logPath)
	if err != nil {
		return "(无日志文件)"
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "(无法读取日志)"
	}
	off := int64(0)
	if st.Size() > int64(max) {
		off = st.Size() - int64(max)
	}
	buf := make([]byte, int(st.Size()-off))
	if _, err := f.ReadAt(buf, off); err != nil {
		return string(buf)
	}
	return string(buf)
}

// debugServeForeground 前台运行:阻塞到 Ctrl+C;同时写状态文件供 --stop 使用。
// 作为后台子进程启动时(serve 默认模式),spawnDetached 会设 TDEBUG_SERVE_LOG 环境变量,
// 状态文件里的日志路径据此填真实值;手动 --foreground 时为空(日志直出终端)。
func debugServeForeground(cfg *debug.Config, cfgPath string) error {
	dir := filepath.Dir(cfgPath)
	if st := runningInstance(dir); st != nil {
		return fmt.Errorf("调试服务已在运行: %s (pid %d),如需重启请先执行 tdebug serve --stop", st.URL, st.PID)
	}
	removeServeInfo(dir) // 清理残留状态

	requested := cfg.Listen
	srv := debug.NewServer(cfg, webFS, cfgPath)
	ln, addr, err := srv.Listen()
	if err != nil {
		return err
	}
	if addr != requested {
		fmt.Printf("[tdebug] 端口 %s 被占用,自动改用 %s\n", requested, addr)
	}
	url := "http://" + addr
	cfg.DataDir = dir
	logPath := os.Getenv("TDEBUG_SERVE_LOG")
	st := &serveInfo{PID: os.Getpid(), URL: url, Log: logPath,
		Since: time.Now().Format(time.RFC3339)}
	_ = writeServeInfo(st, dir)
	defer removeServeInfo(dir)

	fmt.Printf("[tdebug] 服务已启动  前端+API: %s  (pid %d)\n", url, os.Getpid())
	if logPath != "" {
		fmt.Printf("  运行日志: %s\n", logPath)
		fmt.Println("  停止服务: 另开终端执行 tdebug serve --stop")
	} else {
		fmt.Println("  按 Ctrl+C 停止")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := srv.Serve(ctx, ln); err != nil {
		return err
	}
	fmt.Println("[tdebug] 服务已停止")
	return nil
}

// debugServeBackground 默认入口:单实例,后台常驻;实例已运行则打印现状直接返回。
func debugServeBackground(cfg *debug.Config, cfgPath string) error {
	dir := filepath.Dir(cfgPath)
	if st := runningInstance(dir); st != nil {
		fmt.Printf("[tdebug] 调试服务已在后台运行(单实例):\n")
		fmt.Printf("  前端+API: %s  (pid %d)\n", st.URL, st.PID)
		fmt.Printf("  运行日志: %s\n", st.Log)
		fmt.Println("  停止服务: tdebug serve --stop")
		return nil
	}
	removeServeInfo(dir)

	logPath := filepath.Join(dir, serveLogName)
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位可执行文件失败: %w", err)
	}
	args := []string{"serve", "--foreground", "--config", cfgPath}
	if dbgListen != "" {
		args = append(args, "--listen", dbgListen)
	}
	pid, err := spawnDetached(exe, args, logPath)
	if err != nil {
		return fmt.Errorf("启动后台服务失败: %w", err)
	}

	// 等待子进程建好监听并写状态(端口被占用会顺延,需读回真实地址)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if st := runningInstance(dir); st != nil && st.PID == pid {
			fmt.Printf("[tdebug] 服务已在后台启动(单实例):\n")
			fmt.Printf("  前端+API: %s  (pid %d)\n", st.URL, st.PID)
			fmt.Printf("  运行日志: %s\n", st.Log)
			fmt.Println("  停止服务: tdebug serve --stop")
			fmt.Println("  状态查看: tdebug status")
			return nil
		}
		if !pidAlive(pid) {
			return fmt.Errorf("后台服务进程提前退出,日志末尾:\n%s", logTail(logPath, 2000))
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("后台服务 15 秒内未就绪(端口可能全部被占用),日志末尾:\n%s", logTail(logPath, 2000))
}

// debugServeStop 停止后台实例(按状态文件 pid),并清理状态文件。
func debugServeStop() error {
	dir := configDir()
	st, err := readServeInfo(dir)
	if err != nil {
		return fmt.Errorf("没有后台调试服务在运行(未找到状态文件 %s)", serveInfoFile(dir))
	}
	if !pidAlive(st.PID) {
		removeServeInfo(dir)
		return fmt.Errorf("后台调试服务已不在运行(pid %d),已清理状态文件", st.PID)
	}
	if err := killProcess(st.PID); err != nil {
		return fmt.Errorf("停止服务失败(pid %d): %w", st.PID, err)
	}
	for i := 0; i < 50 && pidAlive(st.PID); i++ {
		time.Sleep(200 * time.Millisecond)
	}
	removeServeInfo(dir)
	fmt.Printf("[tdebug] 已停止调试服务 (pid %d)\n", st.PID)
	return nil
}
