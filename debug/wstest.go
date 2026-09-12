package debug

// 接口服务测试:复刻 awsq990「集成服务测试」页签。
// 原版实现:Genero com.HTTPRequest POST(RESTful:doTextRequest;SOAP:SOAPAction:"" + doXmlRequest),
// URL 由接口方式映射 http://<tpserver_ip>/w<zone>/ws/r/awsp9xx。
// 本工具等价实现:经 SSH 在服务器上执行 curl(服务器本机 httpd → ProxyPass → GAS dispatcher),
// 与 awsq990 运行在网络同一侧,不依赖用户本机到 WS 端口的连通性。

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"tdebug/host"
)

// WS_ENDPOINTS 接口方式 → 服务端点(与 awsq990 的 wsfc001 映射一致)
var wsEndpoints = map[string]string{
	"1": "awsp900", // Web service (SOAP)
	"2": "awsp900", // Web service (SOAP,备选入口)
	"3": "awsp920", // RESTful
	"4": "awsp940", // OpenApi restful
	"5": "awsp930", // OpenApi Web service
}

// WSDefaultURLFor 按接口方式与登录区域(→ T100 服务别名)生成默认 URL。
// 前缀口径与 awsq990 一致:出貨區(ZONE=topprd)是 /ws<ZONE>,其余区是 /w<ZONE>。
func WSDefaultURLFor(cfg *Config, mode string) string {
	ep := wsEndpoints[mode]
	if ep == "" {
		ep = "awsp920"
	}
	zone := host.ZoneTNSName(cfg.Zone)
	prefix := "/w"
	if zone == "topprd" {
		prefix = "/ws"
	}
	return "http://127.0.0.1" + prefix + zone + "/ws/r/" + ep
}

// stripWSDLSuffix 去掉 ?wsdl 查询后缀(原生:URL 匹配 http://*?wsdl 即去掉,
// 否则 POST 会打到 WSDL 描述页而不是服务端点)。
func stripWSDLSuffix(url string) string {
	const suffix = "?wsdl"
	if len(url) >= len(suffix) && strings.EqualFold(url[len(url)-len(suffix):], suffix) {
		return url[:len(url)-len(suffix)]
	}
	return url
}

// soapEnvelope 把请求载荷包进 TIPTOP 网关的 SOAP 信封(与 awsq990 btn_test 的 WHEN '1'/'5' 一致:
// soapenv:Envelope → tip:invokeSrv → request)。
// 原生只转义 < 与 >;这里额外转义 &(NewReplacer 单趟替换,先列 & 也不会二次转义),
// 否则载荷里出现 & 会产出非法 XML,网关直接 400。
func soapEnvelope(payload string) string {
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(payload)
	return `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:tip="http://www.digiwin.com.cn/tiptop/TIPTOPServiceGateWay">` + "\n" +
		"   <soapenv:Header/>\n" +
		"   <soapenv:Body>\n" +
		"      <tip:invokeSrv>\n" +
		"         <request>\n" + esc + "\n" +
		"         </request>\n" +
		"      </tip:invokeSrv>\n" +
		"   </soapenv:Body>\n" +
		"</soapenv:Envelope>"
}

// WSTestResult 单次执行结果
type WSTestResult struct {
	HTTPCode    int     `json:"httpCode"`
	DurationSec float64 `json:"durationSec"`
	Response    string  `json:"response"`
	Error       string  `json:"error,omitempty"`
}

// WSTest 执行一次接口调用:报文落服务器临时文件后 curl POST。
// soap=true 时按原生口径把载荷包进 SOAP 信封,并带 SOAPAction:"" 头(与 awsq990 btn_test 一致)。
func WSTest(conn *host.SSHConn, url, body string, soap bool, timeoutSec int) (*WSTestResult, error) {
	url = stripWSDLSuffix(url)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("URL 必须以 http:// 或 https:// 开头")
	}
	if len(body) > 256*1024 {
		return nil, fmt.Errorf("报文超过 256KB")
	}
	// 信封在发送时构造:请求报文区里始终是用户填的原始载荷(与原生一致)
	if soap {
		body = soapEnvelope(body)
	}
	if timeoutSec <= 0 || timeoutSec > 120 {
		timeoutSec = 60
	}
	sftp, err := conn.SFTP()
	if err != nil {
		return nil, err
	}
	// 报文经临时文件传递,避免 shell 转义问题
	bodyFile := fmt.Sprintf("/tmp/tdebug_wstest_%d.body", time.Now().UnixNano()%1000000)
	f, err := sftp.Create(bodyFile)
	if err != nil {
		return nil, fmt.Errorf("写临时报文失败: %w", err)
	}
	if _, err := f.Write([]byte(body)); err != nil {
		f.Close()
		return nil, fmt.Errorf("写临时报文失败: %w", err)
	}
	f.Close()
	defer func() {
		conn.Output("rm -f "+bodyFile, 5*time.Second)
	}()

	// URL 进双引号(curl 侧),剔除引号/反引号/$ 防注入
	safeURL := strings.Map(func(r rune) rune {
		switch r {
		case '"', '`', '$', '\\', '\'':
			return -1
		}
		return r
	}, url)

	cmd := fmt.Sprintf(
		`curl -s -X POST -H 'Content-Type: application/json' --max-time %d --data-binary @%s -w '\n---META---%%{http_code}:%%{time_total}' "%s"`,
		timeoutSec, bodyFile, safeURL)
	if soap {
		cmd = fmt.Sprintf(
			`curl -s -X POST -H 'Content-Type: text/xml; charset=utf-8' -H 'SOAPAction: ""' -H 'User-Agent: Jakarta Commons-HttpClient/3.0.1' --max-time %d --data-binary @%s -w '\n---META---%%{http_code}:%%{time_total}' "%s"`,
			timeoutSec, bodyFile, safeURL)
	}
	// 注意:cmd 内含单引号,不能再用 bash -lc '...' 包一层(嵌套引号会破坏解析);
	// curl 不依赖 T100 环境,直接执行即可
	out, err := conn.Output(cmd, time.Duration(timeoutSec+15)*time.Second)
	res := &WSTestResult{}
	if err != nil && out == "" {
		res.Error = fmt.Sprintf("请求失败: %v", err)
		return res, nil
	}
	// 解析尾部 META(httpcode:耗时)
	idx := strings.LastIndex(out, "---META---")
	if idx >= 0 {
		meta := strings.TrimSpace(out[idx+len("---META---"):])
		out = out[:idx]
		parts := strings.SplitN(meta, ":", 2)
		res.HTTPCode, _ = strconv.Atoi(parts[0])
		if len(parts) > 1 {
			fmt.Sscanf(parts[1], "%f", &res.DurationSec)
		}
	} else {
		res.Error = "无 HTTP 响应(连接失败或超时)"
	}
	res.Response = strings.TrimLeft(out, "\n")
	if len(res.Response) > 256*1024 {
		res.Response = res.Response[:256*1024] + "\n(响应超长已截断)"
	}
	return res, nil
}
