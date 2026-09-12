package debug

import (
	"regexp"
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

// matchFdbErr 返回匹配到的错误行文本
func matchFdbErr(ln string) string {
	for _, re := range fdbErrRes {
		if re.MatchString(ln) {
			return ln
		}
	}
	return ""
}
