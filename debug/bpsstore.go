package debug

// 断点持久化(fgldeb 状态文件的 Go 版):
// <DataDir>/debug-bps/<module>__<prog>.json,记录 file:line + 源码行文本 + 启用态。
// 恢复时按行文本重新定位,源码变更不会把断点恢复到错误位置(fgldeb 同款防护)。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// StoredBP 持久化的断点
type StoredBP struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	LineText string `json:"lineText,omitempty"` // 下断时的源码行文本(重定位依据)
	Func     string `json:"func,omitempty"`
	Enabled  bool   `json:"enabled"`
}

type bpStoreFile struct {
	Module      string     `json:"module"`
	Prog        string     `json:"prog"`
	SavedAt     string     `json:"savedAt"`
	Breakpoints []StoredBP `json:"breakpoints"`
}

var bpNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func bpStorePath(dataDir, module, prog string) string {
	name := bpNameRe.ReplaceAllString(module+"__"+prog, "_")
	return filepath.Join(dataDir, "debug-bps", name+".json")
}

// saveBPs 原子写入断点存档;dataDir 为空时不落盘
func saveBPs(dataDir, module, prog string, bps []StoredBP) error {
	if dataDir == "" {
		return nil
	}
	p := bpStorePath(dataDir, module, prog)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(bpStoreFile{
		Module: module, Prog: prog,
		SavedAt:     time.Now().Format(time.RFC3339),
		Breakpoints: bps,
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// loadBPs 读取断点存档(不存在返回错误,由调用方忽略)
func loadBPs(dataDir, module, prog string) (*bpStoreFile, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("未启用断点持久化")
	}
	data, err := os.ReadFile(bpStorePath(dataDir, module, prog))
	if err != nil {
		return nil, err
	}
	var st bpStoreFile
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}
