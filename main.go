package main

import (
	"embed"

	"tdebug/cli"
)

//go:embed .claude/skills/*.md
var skillFS embed.FS

// webFS 前端构建产物(web/dist);未构建时目录由 .gitkeep 占位,服务返回引导页
//
//go:embed all:web/dist
var webFS embed.FS

func main() {
	cli.Execute(skillFS, webFS)
}
