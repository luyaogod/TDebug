package host

import (
	"regexp"
	"strings"
)

// ---------- 终端协议共享件(登录/探针/会话输出泵共用) ----------

var (
	// ANSI 转义:CSI 序列 + 单字符转义(= > 等 keypad 模式)
	ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]|\x1b[=>]")

	// (fgldb) 提示符:裸提示符(后随 ANSI 残留 = 或 >),命令回显行不算
	rePrompt = regexp.MustCompile(`^\(fgldb\)\s*[=>]?\s*$`)

	// shell 提示符:`<t35prd:/u1/t35prd> `
	ReShellPrompt = regexp.MustCompile(`<[A-Za-z0-9_.@-]+:[^>]*>\s*$`)

	// T100 登录区域菜单提示(两种站点格式:109 那台 `(*)Exit`,金仓这台 `*)Exit`)。
	// 出现即表示菜单就绪,可以敲入区域选项号。
	ReLoginMenu = regexp.MustCompile(`\(\*\)?\s*Exit|\*\)\s*Exit`)
)

// StripANSI 剥离转义序列与残留控制字符(保留 \t)
func StripANSI(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return -1
		}
		return r
	}, s)
}

// IsBarePrompt 判断是否为裸 (fgldb) 提示符(命令回显行不算)
func IsBarePrompt(ln string) bool { return rePrompt.MatchString(ln) }

// LineParser 把 PTY 字节流组装成完整行(处理跨包断行;\r 视为行结束)
type LineParser struct {
	cur strings.Builder
}

// Feed 喂入原始字节,返回已完成的行(已剥离 ANSI/控制字符,已 trim 尾部 \r)
func (p *LineParser) Feed(data []byte) []string {
	var out []string
	for _, b := range data {
		switch b {
		case '\n':
			out = append(out, p.flush())
		case '\r':
			// \r\n 或单独 \r 都视为行结束
			out = append(out, p.flush())
		default:
			p.cur.WriteByte(b)
		}
	}
	return out
}

// PartialStr 返回当前未完成半行的内容(不出行)
func (p *LineParser) PartialStr() string {
	if p.cur.Len() == 0 {
		return ""
	}
	return StripANSI(p.cur.String())
}

// FlushPartial 强制出行半行(用于提示符检测:提示符后面没有换行)
func (p *LineParser) FlushPartial() string {
	if p.cur.Len() == 0 {
		return ""
	}
	return p.flush()
}

// Rest 返回未完成的半行(供会话结束时输出)
func (p *LineParser) Rest() string {
	return p.FlushPartial()
}

func (p *LineParser) flush() string {
	s := StripANSI(p.cur.String())
	p.cur.Reset()
	return strings.TrimRight(s, " ")
}
