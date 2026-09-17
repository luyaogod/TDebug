// 包 cfgfile:config.json 的统一读写入口(读取/校验/修改/原子更新)。
//
// config.json 是 tdebug 唯一配置文件(顶层 debug/mirror/query 等节),serve 与
// 各 CLI 命令都会修改它。本包集中 读取→校验→修改→原子写回 全流程:
// 调用方只声明"改哪些键"与"改前校验",不重复样板,错误语义全工具一致。
package cfgfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Debug 返回 root 的 "debug" 对象节;缺失或非对象返回统一错误。
func Debug(root map[string]any) (map[string]any, error) {
	dbg, _ := root["debug"].(map[string]any)
	if dbg == nil {
		return nil, fmt.Errorf("config.json 缺少 \"debug\" 配置节")
	}
	return dbg, nil
}

// Open 读取并解析 config.json;空文件视为空配置,文件缺失/非法 JSON 报错。
func Open(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败(%s): %w", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("解析配置文件失败(%s): %v", path, err)
	}
	if root == nil {
		root = map[string]any{} // 空文件 → 空配置
	}
	return root, nil
}

// Save 原子写回:MarshalIndent + 临时文件 + 重命名,失败不留半截文件。
// 配置目录不存在时自动创建 —— 缺省位置在 %APPDATA%\T100\tdebug\ 下,新机器上首次保存时该目录还没有。
func Save(path string, root map[string]any) error {
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建配置目录失败(%s): %w", dir, err)
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("更新配置文件失败: %w", err)
	}
	return nil
}

// Edit 统一修改入口:Open → validate(root)(可空)→ mutate(root) → Save。
// validate/mutate 任一失败都不落盘。config.json 的一切修改都应走本函数,
// 避免各调用方重复"读-改-写"样板与错误语义漂移。
func Edit(path string, validate func(root map[string]any) error, mutate func(root map[string]any) error) error {
	root, err := Open(path)
	if err != nil {
		return err
	}
	if validate != nil {
		if err := validate(root); err != nil {
			return err
		}
	}
	if mutate != nil {
		if err := mutate(root); err != nil {
			return err
		}
	}
	return Save(path, root)
}
