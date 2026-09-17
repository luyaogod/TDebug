package debug

// 只读 SQL 的结果解析单测。
//
// 夹具用的是真机(sqlplus)抓下来的原始输出 —— 包括那两处容易被忽略的噪声:
// 登录横幅 GLOBAL_NAME 与 ksql 的表头分隔线。解析错了不会报错,只会**静默给出错的列名**,
// 那比没有结果更坏(看起来是对的)。

import (
	"strings"
	"testing"
)

func TestParseSQLOutOracle(t *testing.T) {
	// 真机原样(登录横幅 + 规则线 + 表头 + 数据行)
	raw := `
GLOBAL_NAME
--------------------------------------------------------------------------------
top109ind/dsdemo@T35PRD
BMAA001 | BMAASTUS
--------- | --------
9WA0-M09H10+43R | Y
9WA0-M09H10+44L | Y
`
	cols, rows, err := parseSQLOut("oracle", raw)
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if strings.Join(cols, ",") != "BMAA001,BMAASTUS" {
		t.Errorf("表头解析错了(横幅没去掉?): %v", cols)
	}
	if len(rows) != 2 {
		t.Fatalf("应有两行,得到 %d: %v", len(rows), rows)
	}
	if rows[0][0] != "9WA0-M09H10+43R" || rows[0][1] != "Y" {
		t.Errorf("首行解析错了: %v", rows[0])
	}
	// 横幅那两行不能被当成数据
	for _, r := range rows {
		for _, v := range r {
			if strings.Contains(v, "GLOBAL_NAME") || strings.Contains(v, "dsdemo@") {
				t.Errorf("登录横幅混进结果里了: %v", r)
			}
		}
	}
}

func TestParseSQLOutKingbase(t *testing.T) {
	// psql -A -F '|' 的形态:表头 + 一条全横线的分隔行 + 数据
	raw := "bmaa001|bmaastus\n--------+--------\nFCPU010100003|Y\n"
	cols, rows, err := parseSQLOut("kingbase", raw)
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if strings.Join(cols, ",") != "bmaa001,bmaastus" {
		t.Errorf("表头解析错了: %v", cols)
	}
	if len(rows) != 1 || rows[0][0] != "FCPU010100003" {
		t.Errorf("数据行解析错了: %v", rows)
	}
}

func TestParseSQLOutErrors(t *testing.T) {
	// 库报错要**报出来**,不能当成"0 行"—— 那会被读成"数据不存在"
	for _, raw := range []string{
		"ORA-00942: table or view does not exist",
		"select * from nope\n*\nERROR at line 1:\nORA-00942: table or view does not exist",
		"ERROR:  relation \"nope\" does not exist",
	} {
		if _, _, err := parseSQLOut("oracle", raw); err == nil {
			t.Errorf("库报错应当返回 error,却被当成结果: %q", raw)
		}
	}
}

func TestParseSQLOutEmpty(t *testing.T) {
	// 0 行不是错误
	for _, raw := range []string{"", "\n\n", "GLOBAL_NAME\n----\nds/x@Y\n"} {
		cols, rows, err := parseSQLOut("oracle", raw)
		if err != nil {
			t.Errorf("%q 不该报错: %v", raw, err)
		}
		if len(cols) != 0 || len(rows) != 0 {
			t.Errorf("%q 应当是空结果,得到 %v %v", raw, cols, rows)
		}
	}
}

// 企业→账号的选择是纯函数,单独钉住:它错了会"上错号",而后果是 0 行 ——
// 看起来和"数据不存在"一模一样。
func TestPickEntAccount(t *testing.T) {
	maps := []EntMapping{{Ent: 99, Account: "dsdemo"}, {Ent: 907, Account: "dsdata"}, {Ent: 2222, Account: "dsstd"}}
	for _, c := range []struct {
		ent  int
		want string
	}{{99, "dsdemo"}, {907, "dsdata"}, {2222, "dsstd"}} {
		got, err := pickEntAccount(maps, c.ent)
		if err != nil || got != c.want {
			t.Errorf("企业 %d 应解析成 %s,得到 %q (err=%v)", c.ent, c.want, got, err)
		}
	}
	// 不在清单里:必须报错,**绝不回退到某个默认账号**
	if _, err := pickEntAccount(maps, 8888); err == nil {
		t.Error("企业不在清单里应当报错,不能静默回退")
	} else if !strings.Contains(err.Error(), "topent") {
		t.Errorf("报错要告诉人怎么改(topent),实际: %v", err)
	}
	// gzou003 是占位符:同样报错
	if _, err := pickEntAccount([]EntMapping{{Ent: 1, Account: "-"}}, 1); err == nil {
		t.Error("账号是占位符时应报错")
	}
}

func TestClampSQLTimeout(t *testing.T) {
	for _, c := range []struct{ in, want int }{{0, sqlTimeoutDefault}, {-1, sqlTimeoutDefault}, {10, 10}, {999, sqlTimeoutMax}} {
		if got := clampSQLTimeout(c.in); got != c.want {
			t.Errorf("clampSQLTimeout(%d) = %d,期望 %d", c.in, got, c.want)
		}
	}
}

// 脚本里那几行是"不设就会出鬼"的,单独钉住别被人顺手删掉。
func TestReadonlyScripts(t *testing.T) {
	ora := oraReadonlyScript("select 1 from dual")
	for _, must := range []string{"set define off", "long 4000", "SET TRANSACTION READ ONLY", "set colsep '|'"} {
		if !strings.Contains(ora, must) {
			t.Errorf("Oracle 脚本缺少 %q —— 少了它 SQL 里的 & 会挂住 / CLOB 会被静默截断", must)
		}
	}
	// READ ONLY 必须在用户语句之前(它得是本事务第一条)
	if strings.Index(ora, "SET TRANSACTION READ ONLY") > strings.Index(ora, "select 1 from dual") {
		t.Error("SET TRANSACTION READ ONLY 必须排在用户语句之前")
	}
	kb := kbReadonlyScript("select 1", 20)
	for _, must := range []string{"SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY", "statement_timeout"} {
		if !strings.Contains(kb, must) {
			t.Errorf("金仓脚本缺少 %q", must)
		}
	}
	// 尾分号不该出现两个
	if strings.Contains(oraReadonlyScript("select 1;"), ";;") {
		t.Error("用户语句的尾分号应被剥掉再加,不能出现两个")
	}
}
