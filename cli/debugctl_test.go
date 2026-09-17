package cli

// exec 批量的单测。
//
// 这里钉的是两个**纯函数** —— 命令列表组装与中止判定。它们是整个批量功能里
// 唯一脱离网络与真实会话还能验证的部分,也恰好是错了会静默出问题的地方:
// 组装错了会漏命令或跑错命令,判定错了会在一个没确认过的现场上继续取值。

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestParseCmdLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"普通多行", "print a\nprint b\n", []string{"print a", "print b"}},
		{"无尾换行的最后一行", "print a\nprint b", []string{"print a", "print b"}},
		{"CRLF", "print a\r\nprint b\r\n", []string{"print a", "print b"}},
		{"UTF-8 BOM", "\xef\xbb\xbfprint a\n", []string{"print a"}},
		{"空行与纯空白行", "print a\n\n   \n\t\nprint b\n", []string{"print a", "print b"}},
		{"注释行", "# 这是注释\nprint a\n", []string{"print a"}},
		{"缩进的 # 也算注释", "   # 注释\nprint a\n", []string{"print a"}},
		{"首尾空白被剔除", "  print a  \n", []string{"print a"}},
		// 命令正文里的 # 必须保留 —— 只有行首的 # 才是注释
		{"正文里的 #", `print "a#b"` + "\n", []string{`print "a#b"`}},
		{"整列都是注释", "# a\n# b\n", nil},
		{"空输入", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseCmdLines([]byte(c.in))
			if len(got) != len(c.want) {
				t.Fatalf("应解析出 %d 条 %v,实际 %d 条 %v", len(c.want), c.want, len(got), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("第 %d 条 = %q,期望 %q", i+1, got[i], c.want[i])
				}
			}
		})
	}
}

func TestBuildCmdList(t *testing.T) {
	// 位置参数在前、文件在后
	got, err := buildCmdList([]string{"print a"}, []byte("print b\nprint c\n"), "cmds.txt")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if strings.Join(got, "|") != "print a|print b|print c" {
		t.Errorf("顺序应为「位置参数在前、文件在后」,实际 %v", got)
	}

	// 只有文件
	got, err = buildCmdList(nil, []byte("print b\n"), "cmds.txt")
	if err != nil || len(got) != 1 || got[0] != "print b" {
		t.Errorf("只用 --file 时应执行文件里的命令,实际 %v (err=%v)", got, err)
	}

	// 空批必须报错,不能悄悄成功
	for _, c := range []struct {
		name string
		args []string
		file []byte
		path string
	}{
		{"都没给", nil, nil, ""},
		{"只有空白位置参数", []string{"  ", ""}, nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := buildCmdList(c.args, c.file, c.path); err == nil {
				t.Error("空批应该报错")
			}
		})
	}

	// 文件给了但一行可执行的都没有:错误要指名文件,否则人会去找命令行哪里写错了
	_, err = buildCmdList(nil, []byte("# 只有注释\n\n  \n"), "cmds.txt")
	if err == nil {
		t.Fatal("空文件应该报错")
	}
	if !strings.Contains(err.Error(), "cmds.txt") {
		t.Errorf("错误里应带上文件路径,实际 %q", err.Error())
	}

	// 上限
	ok := make([]string, maxBatchCmds)
	for i := range ok {
		ok[i] = "print x"
	}
	if _, err := buildCmdList(ok, nil, ""); err != nil {
		t.Errorf("%d 条(正好上限)应放行: %v", maxBatchCmds, err)
	}
	over := make([]string, maxBatchCmds+1)
	for i := range over {
		over[i] = "print x"
	}
	if _, err := buildCmdList(over, nil, ""); err == nil {
		t.Error("超过上限应报错")
	}
}

func TestBatchAbortReason(t *testing.T) {
	cases := []struct {
		name        string
		cmd         string
		softTimeout bool
		state       string // 放行类命令执行后读到的会话态;非放行类命令为 ""
		want        bool
	}{
		// 取值类命令:不动程序位置,永远继续(不需要确认现场)
		{"print", "print g_req_param", false, "", false},
		{"info", "info breakpoints", false, "", false},
		{"break", "break 294", false, "", false},
		{"where", "where", false, "", false},
		{"空命令", "", false, "", false},

		// 放行类命令:停住了就能接着跑 —— 这是"走一步、立刻取一批值"能一次做完的关键
		{"next 后停住", "next", false, "stopped", false},
		{"continue 后命中断点", "continue", false, "stopped", false},
		{"step 后停住", "s", false, "stopped", false},
		{"run 后停在断点", "run", false, "stopped", false},

		// 放行类命令没停住:中止
		{"continue 后仍在跑", "continue", false, "running", true},
		{"run 后程序退出", "run", false, "idle", true},
		{"快照没读到(没能确认)", "next", false, "", true},

		// 大小写与首尾空白:IsResumeCmd 只看首个词并小写化
		{"大写残留", "  CONTINUE  ", false, "running", true},
		// 正文里含关键词**不能**误判 —— 只看首个词
		{"正文含 resume", "print resume", false, "", false},
		{"正文含 continue", "print continue", false, "", false},

		// 软超时一律中止,与命令是什么、状态如何都无关
		{"软超时", "print a", true, "", true},
		{"软超时且命令是 next", "next", true, "stopped", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, why := batchAbortReason(c.cmd, c.softTimeout, c.state)
			if got != c.want {
				t.Errorf("batchAbortReason(%q, soft=%v, state=%q) = %v,期望 %v",
					c.cmd, c.softTimeout, c.state, got, c.want)
			}
			if got && why == "" {
				t.Error("中止时必须给出原因")
			}
			if !got && why != "" {
				t.Errorf("不中止时不该给原因,实际 %q", why)
			}
		})
	}
}

// --set 的点路径赋值。它是"改一个入参再重放"的唯一实现,错了会让重放送出一份
// 形状不对的报文 —— 那比报错难查得多,所以路径不存在时必须当场失败。
func TestSetJSONPath(t *testing.T) {
	base := func() map[string]any {
		var m map[string]any
		if err := json.Unmarshal([]byte(`{
			"digi-header": {"uid": "OA_0002"},
			"digi-body": {"std_data": {"parameter": {"program_code": "apmt530_wf", "page_size": 1000}}},
			"pages": [{"id": "a"}, {"id": "b"}]
		}`), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	cases := []struct {
		name string
		path string
		val  string
		want any // 期望该路径上的新值
	}{
		{"顶层字段", "digi-header", "x", "x"},
		{"深层字段", "digi-body.std_data.parameter.program_code", "other_wf", "other_wf"},
		{"等号后留空 = 清空", "digi-body.std_data.parameter.program_code", "", ""},
		{"值按 JSON 解析成数字", "digi-body.std_data.parameter.page_size", "50", float64(50)},
		{"值按 JSON 解析成布尔", "digi-body.std_data.parameter.program_code", "true", true},
		{"不像 JSON 就当字符串", "digi-body.std_data.parameter.program_code", "abc", "abc"},
		{"数组下标", "pages.1.id", "B", "B"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := base()
			if err := setJSONPath(m, c.path, c.val); err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			// 按同样的路径读回来
			var cur any = m
			for _, seg := range strings.Split(c.path, ".") {
				switch n := cur.(type) {
				case map[string]any:
					cur = n[seg]
				case []any:
					i, _ := strconv.Atoi(seg)
					cur = n[i]
				}
			}
			if cur != c.want {
				t.Errorf("路径 %s = %#v,期望 %#v", c.path, cur, c.want)
			}
		})
	}

	// 路径必须已存在:不自动造层级
	for _, bad := range []struct{ name, path string }{
		{"拼错末段", "digi-body.std_data.parameter.no_such_field"},
		{"拼错中间段", "digi-body.std_data.no_such.parameter"},
		{"数组下标越界", "pages.9.id"},
		{"下标不是数字", "pages.x.id"},
		{"走在标量上", "digi-header.uid.sub"},
	} {
		t.Run("拒绝:"+bad.name, func(t *testing.T) {
			if err := setJSONPath(base(), bad.path, "1"); err == nil {
				t.Errorf("路径 %s 应当报错", bad.path)
			}
		})
	}
}

func TestApplySets(t *testing.T) {
	req := `{"a":{"b":"1"},"c":"2"}`
	out, err := applySets(req, []string{"a.b=改过", "c="})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("改完应当是合法 JSON: %v", err)
	}
	if m["a"].(map[string]any)["b"] != "改过" {
		t.Errorf("a.b 没改上: %v", m)
	}
	if m["c"] != "" {
		t.Errorf("c 应被清成空串: %v", m)
	}

	// 值里含等号时只按第一个等号切
	out, err = applySets(`{"a":"x"}`, []string{"a=k=v"})
	if err != nil || !strings.Contains(out, `"k=v"`) {
		t.Errorf("值里的等号应保留,得到 %s (err=%v)", out, err)
	}

	// 不是 JSON 报文:要说清该用哪个开关
	if _, err := applySets("not json", []string{"a=1"}); err == nil {
		t.Error("非 JSON 报文应当报错")
	} else if !strings.Contains(err.Error(), "--request-file") {
		t.Errorf("错误里应指出可以用 --request-file,实际 %q", err.Error())
	}

	// 写法不对
	if _, err := applySets(`{"a":1}`, []string{"没有等号"}); err == nil {
		t.Error("缺少等号应当报错")
	}
}
