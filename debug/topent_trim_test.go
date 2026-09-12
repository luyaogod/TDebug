package debug

import (
	"encoding/json"
	"strings"
	"tdebug/host"
	"testing"
)

// hTopent 服务端 trim 语义(文本值 + 剔除两侧空白)——纯字符串逻辑验证
func TestTopentTrimSemantics(t *testing.T) {
	cases := map[string]string{
		"  99 ":       "99",
		"\tabc-99_X ": "abc-99_X",
		"   ":         "",
		"99":          "99",
	}
	for in, want := range cases {
		if got := trimTopent(in); got != want {
			t.Fatalf("trimTopent(%q) = %q, want %q", in, got, want)
		}
	}
}

// host.EntValue JSON 兼容:旧配置数字 / 新文本 / null 均可解析;序列化统一为字符串
func TestEntValueJSON(t *testing.T) {
	var c struct {
		Ent host.EntValue `json:"ent"`
	}
	if err := json.Unmarshal([]byte(`{"ent": 99}`), &c); err != nil {
		t.Fatalf("数字 JSON 应可解析: %v", err)
	}
	if c.Ent != "99" {
		t.Fatalf("数字应归一为字符串 %q", c.Ent)
	}
	if err := json.Unmarshal([]byte(`{"ent": "txt-9"}`), &c); err != nil {
		t.Fatalf("文本 JSON 应可解析: %v", err)
	}
	if c.Ent != "txt-9" {
		t.Fatalf("文本应原样保留 %q", c.Ent)
	}
	if err := json.Unmarshal([]byte(`{"ent": null}`), &c); err != nil {
		t.Fatalf("null 应可解析: %v", err)
	}
	if c.Ent != "" {
		t.Fatalf("null 应归一为空 %q", c.Ent)
	}
	out, _ := json.Marshal(&c)
	if string(out) != `{"ent":""}` {
		t.Fatalf("序列化应为字符串: %s", out)
	}
	if n, ok := host.EntValue("42").Int(); !ok || n != 42 {
		t.Fatalf("Int() 应解析数字")
	}
	if _, ok := host.EntValue("txt").Int(); ok {
		t.Fatalf("非数字 Int() 应返回 false")
	}
}

// SetTopent 回执必须锚定「独占一行」:PTY 会把我方命令原样回显,该行含同一标记字符串,
// 用子串匹配会在命令真正执行之前就等到回执(把保存成功建立在假信号上)。
func TestReTopentOKAnchored(t *testing.T) {
	for _, echo := range []string{
		"export TOPENT='99'; echo TDBG-TOPENT-OK",
		"unset TOPENT; echo TDBG-TOPENT-OK",
		"<t35prd:/u1/t35prd>export TOPENT='99'; echo TDBG-TOPENT-OK",
	} {
		if reTopentOK.MatchString(echo) {
			t.Fatalf("命令回显行不应被当成回执: %q", echo)
		}
	}
	if !reTopentOK.MatchString("TDBG-TOPENT-OK") {
		t.Fatal("独占一行的回执应匹配")
	}
	if !reTopentOK.MatchString("  TDBG-TOPENT-OK  ") {
		t.Fatal("回执行两侧空白应容忍")
	}
}

// 下发命令的拼装:空值走 unset,非空值单引号包裹且剔除内嵌单引号(防逃出引号注入)。
func TestTopentShellCmd(t *testing.T) {
	if got := topentShellCmd(""); got != "unset TOPENT; echo "+reTopentOKMark+"\r" {
		t.Fatalf("空值应 unset,got %q", got)
	}
	if got := topentShellCmd("DSCNJ"); got != "export TOPENT='DSCNJ'; echo "+reTopentOKMark+"\r" {
		t.Fatalf("常规值应单引号包裹,got %q", got)
	}
	// 内嵌单引号必须被剔除,否则可提前闭合引号拼接出额外命令
	if got := topentShellCmd("a'; rm -rf /; echo 'b"); strings.Contains(got, "a'; rm") {
		t.Fatalf("内嵌单引号应被剔除,got %q", got)
	}
	// 数字/文本都允许原样下发
	if got := topentShellCmd("99"); !strings.Contains(got, "TOPENT='99'") {
		t.Fatalf("数字值应原样下发,got %q", got)
	}
}
