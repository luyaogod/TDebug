package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install [dir]",
	Short: "安装 Claude Code Agent 技能到项目",
	Long: `将 TDebug 的 Claude Code 技能文件 (tdebug-debug)
安装到目标项目的 .claude/skills/ 目录，使 AI Agent 能自动理解并使用本工具。
不指定目录时安装到当前工作目录。`,
	Example: `  tdebug install
  tdebug install /path/to/project`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := "."
		if len(args) > 0 {
			dir = args[0]
		}

		if skillFS == nil {
			return fmt.Errorf("技能文件未嵌入 (请从源码构建: go build -o tdebug.exe .)")
		}

		targetDir := filepath.Join(dir, ".claude", "skills")
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return fmt.Errorf("创建目录失败 (%s): %w", targetDir, err)
		}

		const skillsPath = ".claude/skills"
		entries, err := fs.ReadDir(skillFS, skillsPath)
		if err != nil {
			return fmt.Errorf("读取技能文件失败: %w", err)
		}
		installed := 0
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			data, err := fs.ReadFile(skillFS, skillsPath+"/"+e.Name())
			if err != nil {
				return fmt.Errorf("读取 %s 失败: %w", e.Name(), err)
			}
			target := filepath.Join(targetDir, e.Name())
			if err := os.WriteFile(target, data, 0o644); err != nil {
				return fmt.Errorf("写入 %s 失败: %w", target, err)
			}
			fmt.Printf("已安装 %s -> %s\n", e.Name(), target)
			installed++
		}

		fmt.Printf("技能安装完成: %d 个文件写入 %s\n", installed, targetDir)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
}
