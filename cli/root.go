package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	configPath string
	useJSON    bool
	verbose    bool
	// skillFS holds the embedded Claude Code skill files (.claude/skills),
	// provided by main via Execute. Used by `tdebug install`.
	skillFS fs.FS
)

// rootCmd is the base command.
var rootCmd = &cobra.Command{
	Use:   "tdebug",
	Short: "TDebug - T100 作业调试器",
	Long: `TDebug 是一个调试 T100 ERP 作业 (4GL/Genero) 的工具。
通过 SSH 在 T100 服务器上驱动 fglrun -d 的 (fgldb) 文本调试协议,
提供本地 Web 调试界面(源码/断点/调用栈/变量/接口日志)与命令行控制端
(tdebug start/exec/...),实现"人操作 GDC 界面 + AI 借助命令行检查分析"的
人机协同调试。

需要 config.json 中的 "debug" 配置节(sshs/zone 等);首次使用先执行
tdebug serve 启动本地调试服务。所有输出使用简体中文 (zh_CN)。`,
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&useJSON, "json", false, "Output in JSON format")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "config.json", "调试配置文件路径 (JSON,含 debug 节)")
}

func formatTriedPaths(paths []string) string {
	var s string
	for _, p := range paths {
		exists := ""
		if _, err := os.Stat(p); err == nil {
			exists = " (found)"
		}
		s += fmt.Sprintf("  - %s%s\n", p, exists)
	}
	return s
}

// resolveConfigPath resolves the debug config file path with the following priority:
//  1. TDEBUG_CONFIG environment variable
//  2. --config flag as absolute path
//  3. --config flag relative to executable directory
//  4. --config flag relative to current working directory
//
// Returns an error if no existing file can be found at any of these locations.
func resolveConfigPath(flagPath string) (string, error) {
	var candidates []string

	// 1. TDEBUG_CONFIG environment variable (highest priority)
	if env := os.Getenv("TDEBUG_CONFIG"); env != "" {
		candidates = append(candidates, env)
	}

	// 2. --config flag as-is
	candidates = append(candidates, flagPath)

	// 3. --config flag relative to executable directory
	if !filepath.IsAbs(flagPath) {
		if execPath, err := os.Executable(); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(execPath), flagPath))
		}
	}

	// 4. --config flag relative to CWD
	if !filepath.IsAbs(flagPath) {
		if cwd, err := os.Getwd(); err == nil {
			candidates = append(candidates, filepath.Join(cwd, flagPath))
		}
	}

	// Try each candidate
	var tried []string
	for _, p := range candidates {
		abs, _ := filepath.Abs(p)
		if _, err := os.Stat(abs); err == nil {
			return abs, nil
		}
		tried = append(tried, abs)
	}

	// None found - give a helpful error
	return "", fmt.Errorf(
		"配置文件未找到。\n\n尝试了以下路径:\n%s\n\n设置 TDEBUG_CONFIG 环境变量或使用 --config 指定正确路径:\n  setx TDEBUG_CONFIG \"D:\\path\\to\\config.json\"\n  tdebug --config \"D:\\path\\to\\config.json\" status",
		formatTriedPaths(tried),
	)
}

// Execute runs the root command. skills carries the embedded Claude Code
// skill files (used by `tdebug install`); it may be nil when unavailable.
// web carries the embedded debug web frontend (web/dist); may be nil/empty.
func Execute(skills fs.FS, web fs.FS) {
	skillFS = skills
	webFS = web
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// IsJSON returns true if JSON output is requested.
func IsJSON() bool { return useJSON }
