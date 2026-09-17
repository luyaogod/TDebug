package debug

import (
	"regexp"
	"strings"
)

// ---------- fgldb 协议锚点正则(全部来自 172.16.1.09 真机实测输出) ----------
// 终端共享件(LineParser/提示符判定/ANSI 剥离)已迁 tdebug/host 包。

var (
	// 断点命中:`Breakpoint 1, asf_bsft001_wf.bsft001_wf_construct() at asf_bsft001_wf.4gl:4452`
	reBreakHit = regexp.MustCompile(`^Breakpoint (\d+), (.+?) at (.+?):(\d+)\s*$`)
	// 人工中断(SIGINT 后;终端 ^C 回显可能与字样合并为一行,用子串匹配)
	reInterrupt = regexp.MustCompile(`INTERRUPT`)
	// 步进/位置头:`cl_ap_code_fuzzyquery() at lib_cl_ap_code.4gl:4593`(函数变化时出现)
	reStopHeader = regexp.MustCompile(`^([A-Za-z_][\w.]*)\(\) at (.+?):(\d+)\s*$`)
	// 调用栈帧:`#0 asf_bsft001_wf.bsft001_wf_construct() at asf_bsft001_wf.4gl:4452`
	reFrame = regexp.MustCompile(`^#(\d+)\s+(.+?)\s+at\s+(.+?):(\d+)\s*$`)
	// 断点设置成功:`Breakpoint 1 at 0x00000000: file asf_bsft001_wf.4gl, line 4452.`
	reBPSet = regexp.MustCompile(`^Breakpoint (\d+) at \S+: file (.+), line (\d+)\.`)
	// print 结果:`$1 = "CNJ-ICC-250800000012"`(记录/数组为多行)
	rePrintVal = regexp.MustCompile(`^\$(\d+) = (.*)$`)
	// 变量不存在:`No symbol "lp_str" in current context.`
	reNoSymbol = regexp.MustCompile(`No symbol "(.+)" in current context`)
	// 恢复运行
	reContinuing = regexp.MustCompile(`^Continuing\.`)
	// 停站上下文中的源码行:`   4452   LET ...` / `-> 4452   LET ...`
	reSource = regexp.MustCompile(`^(->)?\s*(\d+)\s{2,}(.*)$`)
	// 步进的紧凑停站(源码不可用时):`89	 in lib_cl_ap.4gl`
	reCompactStep = regexp.MustCompile(`^(\d+)\s+in\s+(\S+)\s*$`)
	// info breakpoints 表行:`1   breakpoint     keep y   0x00000000 in asf_bsft001_wf.xx at asf_xx.4gl:4452`
	reBPInfo = regexp.MustCompile(`^(\d+)\s+breakpoint\s+keep\s+(\w)\s+\S+\s+in\s+(\S+)\s+at\s+(.+):(\d+)`)
	// info locals 变量行:`lp_str = "CNJ-ICC-250800000012"`(记录/数组值可续行)
	reLocalVar = regexp.MustCompile(`^([A-Za-z_][\w.]*)\s*=\s*(.*)$`)
	// info variables 声明行(fgldb 实测为 `Globals:` 头 + `名字 类型` 两列,非 name=value)
	reGlobalDecl = regexp.MustCompile(`^([A-Za-z_][\w.]*)\s+([A-Za-z_][\w.\[\], ]*)$`)
	// info functions 函数行:`bsft001_wf_construct ()`
	reFuncList = regexp.MustCompile(`^([A-Za-z_][\w.]*)\s*\(\s*\)\s*$`)
	// info line 位置行:`Line 801 of "asf_bsft001_wf.4gl" starts at address 0x0 <main+0>`
	reInfoLine = regexp.MustCompile(`^Line (\d+) of "([^"]+)"`)
	// until 目标行不存在:`No line 999999 in file asf_bsft001_wf.`
	reNoLine = regexp.MustCompile(`^No line \d+ in file`)
	// until 目标文件不存在:`No source file named bsft001_wf.4gl.`
	reNoSourceFile = regexp.MustCompile(`^No source file named`)
	// 程序退出:`Program exited normally.` / `Program exited with code N.`(作业窗口被关闭或正常结束)
	reProgramExited = regexp.MustCompile(`^Program exited`)
)

// fdbErrRes 已知的调试器错误行(出现在响应中时转成命令错误)
var fdbErrRes = []*regexp.Regexp{
	reNoSymbol,
	regexp.MustCompile(`^The program is not being run`),
	regexp.MustCompile(`^No stack\.`),
	regexp.MustCompile(`^Cannot execute this command`),
	regexp.MustCompile(`^Program not being run`),
	regexp.MustCompile(`^invalid argument`),
	reNoLine,
	reNoSourceFile,
}

// resumeCmds 会**让程序跑起来**的命令:发出后程序就不在调试器上了,直到下一次停站
// (或者一直不停)。用于两处关键判定:
//   - 发出后必须如实把会话状态翻成 running(否则状态在说谎:界面上还显示"已停站",
//     interrupt 也会被自己的 state==Running 检查拒掉,卡住时连中断都发不出去)
//   - 只有这类命令在飞时,才把带 `->` 的源码行当成一次停站(见 onLine)
//
// 含 fgldb 的常用缩写;内部调用方(Step/Continue/Run)用的都是全名,缩写只有 /raw 透传才可能出现。
var resumeCmds = map[string]bool{
	"run": true, "continue": true, "c": true,
	"next": true, "step": true, "s": true,
	"until": true, "finish": true,
}

// IsResumeCmd 判断一条 fgldb 命令是否会放行程序(只看首个词,忽略参数)
func IsResumeCmd(cmd string) bool {
	f := strings.Fields(strings.TrimSpace(cmd))
	if len(f) == 0 {
		return false
	}
	return resumeCmds[strings.ToLower(f[0])]
}

// bpCmds 会改动断点集合的 fgldb 命令。
//
// 为什么要单列:Ai 走 `exec "break X"`(即 /raw,kind="other"),而断点缓存 s.bps
// 只由 `exec(kind="break")` 那条路径维护(onLine 按 pending.kind 分派解析)。
// 不透传这条命令的话,AI 下的断点**在会话里等于不存在** —— 快照里是空的(前端断点列表
// 空白)、persistBPs 也读不到它(存不下来)。
var bpCmds = map[string]bool{
	"break": true, "tbreak": true, "clear": true,
	"delete": true, "disable": true, "enable": true,
}

// IsBreakpointCmd 判断一条 fgldb 命令会不会改动断点集合(只看首个词)
func IsBreakpointCmd(cmd string) bool {
	f := strings.Fields(strings.TrimSpace(cmd))
	if len(f) == 0 {
		return false
	}
	return bpCmds[strings.ToLower(f[0])]
}

// matchFdbErr 返回匹配到的错误行文本
func matchFdbErr(ln string) string {
	for _, re := range fdbErrRes {
		if re.MatchString(ln) {
			return ln
		}
	}
	return ""
}

// ---------- 交互语句分类 ----------
//
// fgldb 协议看不到前端界面:程序执行到交互语句就会阻塞在用户界面上等人操作,
// 而调试器里它只是 running —— 与死循环在协议层无法区分。唯一可靠的证据是
// 「程序停在哪一行」:停在这类语句上,就是在等用户。
// 官方文档两处实证:attach 示例直接打出 `108  DISPLAY ARRAY contlist TO sr.*`,
// Ctrl-C 示例打出 `-> 2  MENU "Test"`。
//
// 判定顺序有讲究:复合形式必须排在裸形式之前(INPUT ARRAY / INPUT BY NAME 先于 INPUT)。
var interactiveKeywords = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"input_array", regexp.MustCompile(`(?i)^INPUT\s+ARRAY\b`)},
	{"input_by_name", regexp.MustCompile(`(?i)^INPUT\s+BY\s+NAME\b`)},
	{"input", regexp.MustCompile(`(?i)^INPUT\b`)},
	{"display_array", regexp.MustCompile(`(?i)^DISPLAY\s+ARRAY\b`)},
	{"menu", regexp.MustCompile(`(?i)^MENU\b`)},
	// DIALOG 是 Genero 的通用交互指令(INPUT/MENU/CONSTRUCT 的现代写法),
	// 也是 T100 客制里最常见的那种 —— 真机验证时程序正停在
	// `DIALOG ATTRIBUTES(UNBUFFERED,FIELD ORDER FORM)` 上,漏了它这一整类就全判不出来。
	{"dialog", regexp.MustCompile(`(?i)^DIALOG\b`)},
	{"construct", regexp.MustCompile(`(?i)^CONSTRUCT\b`)},
	{"prompt", regexp.MustCompile(`(?i)^PROMPT\b`)},
	{"window", regexp.MustCompile(`(?i)^OPEN\s+WINDOW\b`)},
}

// ClassifyInteractiveLine 判断一行 4GL 源码是否为「会停下来等用户操作」的交互语句,
// 返回语句类别(input|input_by_name|input_array|display_array|menu|construct|prompt|window),
// 不是交互语句返回空串。
// 先剥字符串与注释再匹配,避免 `DISPLAY 'INPUT'`、`#add-point:...INPUT` 之类误命中。
func ClassifyInteractiveLine(text string) string {
	code := stripQuoted(text)
	if i := strings.Index(code, "--"); i >= 0 { // 4GL 行注释
		code = code[:i]
	}
	if i := strings.Index(code, "#"); i >= 0 { // 预处理指令(#add-point 之类),与 lineVarNames 同款截断
		code = code[:i]
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	for _, k := range interactiveKeywords {
		if k.re.MatchString(code) {
			return k.kind
		}
	}
	return ""
}

// CurSourceText 取停站源码块里当前行(`->` 箭头那行)的原文;无当前行标记时返回空串。
func CurSourceText(src []SourceLine) string {
	for _, sl := range src {
		if sl.IsCur {
			return sl.Text
		}
	}
	return ""
}
