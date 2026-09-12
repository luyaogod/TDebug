package debug

import (
	"strings"
	"testing"
)

// SOAP 信封:与 awsq990 btn_test 的 WHEN '1'/'5' 同形(Envelope → invokeSrv → request),
// 且转义不会二次转义(载荷含 & 时不能产出 &amp;lt; 这类双重转义)。
func TestSOAPEnvelope(t *testing.T) {
	got := soapEnvelope(`{"a":"x&y","b":"<t>"}`)
	for _, want := range []string{
		`xmlns:tip="http://www.digiwin.com.cn/tiptop/TIPTOPServiceGateWay"`,
		"<tip:invokeSrv>",
		"<request>",
		"</request>",
		"</soapenv:Envelope>",
		`{"a":"x&amp;y","b":"&lt;t&gt;"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("信封缺少片段 %q\n实际:\n%s", want, got)
		}
	}
	// 双重转义检查:& 先被替换成 &amp;,其中的 & 不能再被当成分隔符处理
	if strings.Contains(got, "&amp;lt;") || strings.Contains(got, "&amp;amp;") {
		t.Errorf("出现二次转义:\n%s", got)
	}
}

// ?wsdl 后缀剥离(原生:URL 匹配 http://*?wsdl 即去掉)
func TestStripWSDLSuffix(t *testing.T) {
	cases := map[string]string{
		"http://h:8080/wt35prd/ws/r/awsp900?wsdl": "http://h:8080/wt35prd/ws/r/awsp900",
		"http://h:8080/wt35prd/ws/r/awsp900?WSDL": "http://h:8080/wt35prd/ws/r/awsp900",
		"http://h:8080/wt35prd/ws/r/awsp920":      "http://h:8080/wt35prd/ws/r/awsp920",
		"http://h:8080/wt35prd/ws/r/awsp920?x=1":  "http://h:8080/wt35prd/ws/r/awsp920?x=1",
	}
	for in, want := range cases {
		if got := stripWSDLSuffix(in); got != want {
			t.Errorf("stripWSDLSuffix(%q) = %q,期望 %q", in, got, want)
		}
	}
}

// 默认地址:接口方式映射 + 出貨區(topprd)的 /ws 前缀特例(原生 2528-2531)
func TestWSDefaultURLFor(t *testing.T) {
	cases := []struct {
		zone, mode, want string
	}{
		{"36", "3", "http://127.0.0.1/wt35prd/ws/r/awsp920"},
		{"36", "1", "http://127.0.0.1/wt35prd/ws/r/awsp900"},
		{"36", "4", "http://127.0.0.1/wt35prd/ws/r/awsp940"},
		{"36", "5", "http://127.0.0.1/wt35prd/ws/r/awsp930"},
		{"t", "3", "http://127.0.0.1/wstopprd/ws/r/awsp920"}, // 出貨區:/ws 前缀
		{"35", "3", "http://127.0.0.1/wt35tst/ws/r/awsp920"},
		{"36", "9", "http://127.0.0.1/wt35prd/ws/r/awsp920"}, // 未知方式回落 awsp920
	}
	for _, c := range cases {
		cfg := &Config{Zone: c.zone}
		if got := WSDefaultURLFor(cfg, c.mode); got != c.want {
			t.Errorf("zone=%s mode=%s 得到 %q,期望 %q", c.zone, c.mode, got, c.want)
		}
	}
}
