package cli

// 桌面模式(Electron 外壳专用入口):
//   - 与 `serve --foreground` 同一套服务流程,但面向"被桌面壳拉起的子进程":
//     允许配置文件不存在(首启自动写默认骨架)、允许 sshs 为空(先起来再加环境)、
//     就绪后在 stdout 打印一行 TDEBUG_READY {json} 让壳拿到真实地址;
//   - 单实例/状态文件/端口顺延与 serve 完全一致:桌面实例照写 .tdebug-serve.json,
//     所以 CLI(status/start/exec…)与 AI 技能能自动发现并驱动桌面版的会话;
//   - 已有实例在跑时打印 READY{attached:true} 后立即退出:壳直接接管既有实例,
//     退出时也不去停它(避免"壳一关,别人起好的服务也没了")。

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"tdebug/cfgfile"
	"tdebug/debug"
)

// desktopDefaultListen 桌面版首启写入 config.json 的默认监听地址。
// 与 CLI 默认(28670)分开,便于 CLI 与桌面版各跑各的;端口被占仍会自动顺延。
const desktopDefaultListen = "127.0.0.1:28675"

var (
	desktopListen  string // 空 = 用配置里的 debug.listen(首启建配置时写 desktopDefaultListen)
	desktopDataDir string // 空 = 用配置文件所在目录
)

var desktopCmd = &cobra.Command{
	Use:   "desktop",
	Short: "桌面模式:为 Electron 壳启动本地服务(自建数据目录/配置,前台阻塞,打印 READY 行)",
	Long: `桌面模式:供 Electron 桌面壳拉起,前台阻塞运行本地服务(界面与 API 与 CLI 完全相同)。

与 serve 的区别:
  · 配置文件不存在时自动建数据目录并写入默认 config.json(便于桌面版首启);
  · 允许尚未配置服务器环境(sshs 为空),先起服务再去「设置 → 环境」添加;
  · 就绪后在 stdout 打印一行 ` + desktopReadyMark + `{json}(url/pid/config/log),
    桌面壳据此加载界面;已有实例在跑则打印 attached:true 并退出,由壳接管。

数据目录 = 配置文件所在目录(断点持久化、服务状态/日志都落这里);
默认监听 ` + desktopDefaultListen + `(可在设置页改,端口被占用会自动顺延)。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, err := resolveDesktopConfigPath()
		if err != nil {
			return err
		}
		if desktopDataDir != "" {
			abs, err := filepath.Abs(desktopDataDir)
			if err != nil {
				return fmt.Errorf("数据目录非法(%s): %w", desktopDataDir, err)
			}
			cfgPath = filepath.Join(abs, filepath.Base(cfgPath))
		}
		dir := filepath.Dir(cfgPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建数据目录失败(%s): %w", dir, err)
		}
		if _, statErr := os.Stat(cfgPath); os.IsNotExist(statErr) {
			// 首启:优先从旧位置迁移(桌面版旧目录 %APPDATA%\TDebug、exe 同目录),
			// 迁移不到才写默认骨架。壳每次都传 --config,走不到 CLI 的迁移分支,故在此兜住。
			if src := migrateLegacyConfigTo(cfgPath); src != "" {
				if !IsJSON() {
					fmt.Printf("[tdebug] 已迁移配置到 %s (来自 %s)\n", cfgPath, src)
				}
			} else {
				if err := writeDesktopDefaultConfig(cfgPath, desktopListen); err != nil {
					return err
				}
				if !IsJSON() {
					fmt.Printf("[tdebug] 已创建默认配置: %s\n", cfgPath)
				}
			}
		}
		cfg, err := debug.LoadConfigAllowEmpty(cfgPath)
		if err != nil {
			return err
		}
		if len(cfg.SSHs) == 0 {
			fmt.Fprintln(os.Stderr, "[tdebug] 尚未配置服务器环境:请在界面「设置 → 环境」添加后再启动调试")
		}
		if desktopListen != "" {
			cfg.Listen = desktopListen
		}
		cfg.DataDir = dir
		// 已有实例(同数据目录):交给壳接管,不新起进程
		if st := runningInstance(dir); st != nil {
			printDesktopReady(desktopReady{URL: st.URL, PID: st.PID, Config: cfgPath, Log: st.Log, Attached: true})
			return nil
		}
		return runServe(cfg, cfgPath, serveOpts{readyJSON: true, quiet: IsJSON()})
	},
}

// resolveDesktopConfigPath 桌面模式定位配置文件:优先级与 CLI 一致,但**允许文件不存在**
// (首启由本命令写默认骨架),因此不套 resolveConfigPath 的"必须已存在"校验。
// 壳显式传了 --config 时以它为准:打包后的 tdebug.exe 位于 resources\ 下,认不到
// exe 同目录的 .portable 标记,不能退回默认落点(否则便携版会把配置写进用户目录)。
func resolveDesktopConfigPath() (string, error) {
	if env := os.Getenv("TDEBUG_CONFIG"); env != "" {
		return filepath.Abs(env)
	}
	if p, err := resolveConfigPath(configPath); err == nil {
		return p, nil
	}
	if configPath != "" {
		return filepath.Abs(configPath)
	}
	return defaultConfigPath(), nil
}

// writeDesktopDefaultConfig 写默认配置骨架:默认值统一取 debug.NewDefaultConfig()
// (与 fillDefaults 同源,不重复硬编码),sshs 留空由用户在界面里添加。
func writeDesktopDefaultConfig(path, listen string) error {
	cfg := debug.NewDefaultConfig()
	if listen != "" {
		cfg.Listen = listen
	} else {
		cfg.Listen = desktopDefaultListen
	}
	return cfgfile.Save(path, map[string]any{"debug": cfg})
}

func init() {
	desktopCmd.Flags().StringVar(&desktopListen, "listen", "", "覆盖监听地址(默认取配置;首启建配置写 "+desktopDefaultListen+")")
	desktopCmd.Flags().StringVar(&desktopDataDir, "data-dir", "", "数据目录(默认取配置文件所在目录)")
	rootCmd.AddCommand(desktopCmd)
}
