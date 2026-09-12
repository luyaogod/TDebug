package host

import (
	"strings"
	"testing"
)

// 探针取值行只回显真实变量名(TOP=... 等),不再有 TDICT_ 这类假变量名前缀。
func TestReTEnvKVRealNames(t *testing.T) {
	cases := []struct{ line, key, val string }{
		{"TOP=/u1/t35prd", "TOP", "/u1/t35prd"},
		{"TOPENT=99", "TOPENT", "99"},
		{"TOPENT=DSCNJ", "TOPENT", "DSCNJ"},
		{"TOPENT=", "TOPENT", ""}, // 登录未导出该变量:回显为空是有效信息
		{"ERP=/u1/t35prd/erp", "ERP", "/u1/t35prd/erp"},
		{"COM=/u1/t35prd/com", "COM", "/u1/t35prd/com"},
		{"FGLDIR=/u1/genero/fgl", "FGLDIR", "/u1/genero/fgl"},
		{"FGLRESOURCEPATH=/a:/b", "FGLRESOURCEPATH", "/a:/b"},
	}
	for _, c := range cases {
		m := reTEnvKV.FindStringSubmatch(c.line)
		if m == nil {
			t.Fatalf("%q 应匹配 reTEnvKV", c.line)
		}
		if m[1] != c.key || m[2] != c.val {
			t.Fatalf("%q 解析为 %q=%q,期望 %q=%q", c.line, m[1], m[2], c.key, c.val)
		}
	}
	// TOP 是 TOPENT 的前缀:服务器自己打印的 "TOPENT   = 99"(等号两侧带空格)不应被采纳
	if m := reTEnvKV.FindStringSubmatch("TOPENT   = 99"); m != nil {
		t.Fatalf("带空格的服务器输出不应匹配取值行,got %v", m)
	}
}

// 只有 TEnvBegin/TEnvEnd 之间的取值行才被采纳:
// PTY 回显的整条命令(含定界符文本)与终端重绘碎片都落在区间外。
func TestTEnvParserGating(t *testing.T) {
	var p TEnvParser
	lines := []string{
		// 命令回显:含定界符与取值文本,但不是独立定界符行,必须全部忽略
		"<t35prd:/u1/t35prd>echo TDBG-BEGIN; echo TOP=$TOP; echo TOPENT=${TOPENT-}; echo TDBG-END",
		// 终端换行/重绘碎片(真实故障现象里出现过这种行)
		"DICT_COM=$COM;    <EPATH; echo TDBG-END",
		"TOP=/should-be-ignored",
		"TOPENT=should-be-ignored",
		TEnvBegin,
		"TOP=/u1/t35prd",
		"ERP=/u1/t35prd/erp",
		"COM=/u1/t35prd/com",
		"FGLDIR=/u1/genero/fgl",
		"FGLRESOURCEPATH=/u1/t35prd/erp:/u1/t35prd/com",
		"TOPENT=99",
		TEnvEnd,
		"TOPENT=after-end-must-be-ignored",
	}
	done := false
	for _, ln := range lines {
		if p.Feed(ln) {
			done = true
			break
		}
	}
	if !done {
		t.Fatal("应见到结束定界符")
	}
	env := p.Env()
	if env.TOP != "/u1/t35prd" || env.ERP != "/u1/t35prd/erp" || env.COM != "/u1/t35prd/com" {
		t.Fatalf("区间内取值解析错误: %+v", env)
	}
	if env.FGLDIR != "/u1/genero/fgl" || env.FGLResourcePath != "/u1/t35prd/erp:/u1/t35prd/com" {
		t.Fatalf("区间内取值解析错误: %+v", env)
	}
	if env.Topent != "99" {
		t.Fatalf("TOPENT 应为区间内的 99,got %q", env.Topent)
	}
	if !env.Valid() {
		t.Fatal("TOP/ERP 齐备时应判定有效")
	}
}

// 探针只在定界符之间回显真实变量名,不应再出现 TDICT_ 前缀。
func TestTEnvProbeShape(t *testing.T) {
	probe := TEnvProbe()
	for _, want := range []string{"echo TOP=$TOP", "echo ERP=$ERP", "echo COM=$COM", "echo FGLDIR=$FGLDIR", "echo FGLRESOURCEPATH=$FGLRESOURCEPATH", "echo TOPENT=${TOPENT-}"} {
		if !strings.Contains(probe, want) {
			t.Fatalf("探针缺少 %q: %s", want, probe)
		}
	}
	if strings.Contains(probe, "TDICT_") {
		t.Fatalf("探针不应再回显 TDICT_ 假变量名: %s", probe)
	}
	if !strings.Contains(probe, TEnvBegin) || !strings.Contains(probe, TEnvEnd) {
		t.Fatalf("探针应含两个定界符: %s", probe)
	}
}

// TOPENT 是可选信息:缺失或为空不影响动态环境是否可用,
// 否则登录回读会因该变量未设置而整体失败(T100 路径无静态兜底)。
func TestRuntimeEnvValidIgnoresTopent(t *testing.T) {
	full := &RuntimeEnv{TOP: "/u1/t35prd", ERP: "/u1/t35prd/erp"}
	if !full.Valid() {
		t.Fatal("TOP/ERP 齐备时应判定有效(TOPENT 为空不影响)")
	}
	onlyTopent := &RuntimeEnv{Topent: "99"}
	if onlyTopent.Valid() {
		t.Fatal("只有 TOPENT 不应判定有效")
	}
}
