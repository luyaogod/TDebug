package debug

import (
	"encoding/json"
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
