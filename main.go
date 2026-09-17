package main

import (
	"embed"

	"tdebug/cli"
)

// webFS 前端构建产物(web/dist);未构建时目录由 .gitkeep 占位,服务返回引导页
//
//go:embed all:web/dist
var webFS embed.FS

// AI 技能文件不再内嵌:以 skills/ 目录随发行包分发(与 TDictCli/TDev 一致),
// 由 `tdebug install skills` 复制到目标目录。改技能内容不需要重新编译。
func main() {
	cli.Execute(webFS)
}
