package debug

import "testing"

// TestEntryStopInfoFile 入口停站必须带上源文件名 —— 前端"起好会话、页面再接上来"
// 走的是 syncFromSessions,它按 stop.file 去取源码;这里不给名字,代码区就一片空白。
//
// 回归的正是 WS 服务程序那一类:wssp*/awsp* 不在 gzzz_t 里(实测 wssp% 命中 0 条),
// 按作业名解析实体程序必定落空 → RunProg 为空。以前只看 RunProg,于是所有
// `tdebug wsdebug <rowid>` 重放的会话入口都没有文件名。
func TestEntryStopInfoFile(t *testing.T) {
	cases := []struct {
		name                       string
		module, prog, runProg, cust string
		want                       string
	}{
		{
			name: "实体程序已解析:沿用 gzzz_t 的结果",
			module: "apm", prog: "apmt520_wf", runProg: "apmt520",
			want: "apm_apmt520.4gl",
		},
		{
			// 这条就是本次修的:WS 服务程序解析不到实体程序,退回启动用的程序名
			name: "WS 服务程序:RunProg 为空则退回 Prog",
			module: "wss", prog: "wssp01131", runProg: "",
			want: "wss_wssp01131.4gl",
		},
		{
			name: "客制模块:用 custModule 前缀",
			module: "apm", prog: "apmt520_wf", runProg: "apmt520", cust: "cpm",
			want: "cpm_apmt520.4gl",
		},
		{
			name: "客制模块 + RunProg 为空",
			module: "wss", prog: "wssp01131", runProg: "", cust: "cws",
			want: "cws_wssp01131.4gl",
		},
		{
			name: "模块未知:不给文件名(前端另有兜底)",
			module: "", prog: "wssp01131", runProg: "",
			want: "",
		},
		{
			name: "程序名也没有:不给文件名",
			module: "wss", prog: "", runProg: "",
			want: "",
		},
		{
			name: "模块名不合法(防注入白名单):不给文件名",
			module: "wss;id", prog: "wssp01131", runProg: "",
			want: "",
		},
		{
			// RunProg 有值但 Prog 为空:仍以 RunProg 为准
			name: "只有 RunProg 有值",
			module: "apm", prog: "", runProg: "apmt520",
			want: "apm_apmt520.4gl",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Session{Module: c.module, Prog: c.prog, RunProg: c.runProg, custModule: c.cust}
			got := s.entryStopInfo()
			if got.Reason != "entry" {
				t.Fatalf("Reason = %q,期望 entry", got.Reason)
			}
			if got.File != c.want {
				t.Fatalf("File = %q,期望 %q", got.File, c.want)
			}
		})
	}
}

// TestEntryStopInfoFileResolves 合成的文件名必须能被源码查找器解析到真实路径上。
// 只看 File 字段不够 —— 名字对不上候选路径的话,前端照样空白,只是错得更隐蔽。
func TestEntryStopInfoFileResolves(t *testing.T) {
	s := &Session{Module: "wss", Prog: "wssp01131", RunProg: ""}
	file := s.entryStopInfo().File
	if file != "wss_wssp01131.4gl" {
		t.Fatalf("File = %q,期望 wss_wssp01131.4gl", file)
	}
	// 模块自有目录(4gl/42m)优先,所以第一个候选应当就是去掉模块前缀的母版名
	roots := []string{"/u1/t35prd/com"}
	cands := sourceCandidatePaths(roots, "wss", file)
	if len(cands) == 0 || cands[0] != "/u1/t35prd/com/wss/4gl/wssp01131.4gl" {
		t.Fatalf("首个候选 = %v,期望 /u1/t35prd/com/wss/4gl/wssp01131.4gl", cands)
	}
}
