package safesql

// 只读白名单的单测。
//
// 这是安全边界:它放行一条语句,就意味着这条语句会被送到正式区的库上执行。
// 所以用例分两半 —— "该放行的不能被误杀"(误杀会逼着人绕开工具)与
// "该拒的一条都不能漏"(漏了就是真出事),绕过尝试那一组尤其要看。

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"普通语句", "select a from t", "select a from t"},
		{"空白折叠", "select  a\n\tfrom   t", "select a from t"},
		{"尾分号剥掉", "select a from t;", "select a from t"},
		{"行注释剥掉", "select a -- 这是注释\nfrom t", "select a from t"},
		{"块注释剥掉", "select /*x*/ a from t", "select a from t"},
		{"hint 剥掉", "select /*+ INDEX(t) */ a from t", "select a from t"},
		{"字面量里的 -- 不剥", "select 'a--b' from dual", "select '' from dual"},
		{"字面量里的分号不剥", "select 'a;b' from dual", "select '' from dual"},
		{"'' 是转义", "select 'it''s' from dual", "select '' from dual"},
		{"双引号标识符", `select "MY COL" from t`, `select "" from t`},
		{"Oracle q 引号", "select q'[x--y]' from dual", "select '' from dual"},
		{"q 引号带分号", "select q'{a;b}' from dual", "select '' from dual"},
		{"注释夹在词之间要留空格", "select a/*x*/b from t", "select a b from t"},
		{"BOM 剥掉", "\ufeffselect a from t", "select a from t"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Normalize(c.in); got != c.want {
				t.Errorf("Normalize(%q)\n 得到 %q\n 期望 %q", c.in, got, c.want)
			}
		})
	}
}

func TestCheckAccepts(t *testing.T) {
	good := []string{
		"select 1 from dual",
		"SELECT a,b FROM t WHERE x=1",
		"select a from t;",
		"select a from t;\n",
		"with c as (select 1 from dual) select * from c",
		"WITH c AS (SELECT 1 FROM dual) SELECT * FROM c",
		// 关键词/分号出现在**字面量里**时不能误杀
		"select 'delete from t' from dual",
		"select 'a;b' from dual",
		"select q'[drop table t]' from dual",
		"select 1 from t where col='O''Brien'",
		"select nvl(a,'-') from t",
		"select /*+ INDEX(t) */ a from t",
		`select "MY COL", "SELECT" from t`,
		"select * from bmaa_t where bmba019 <> '2'",
		"select a from t where b like '%DELETE%'",
		"select count(*) from wsfa_t",
		"select t.a, u.b from imaa_t t join ooef_t u on t.a=u.b where t.c=1",
		// 词内包含关键词不该误杀
		"select deleted_flag, updated_at, insertion_no from t",
		"select set_no, offset_value from t",
	}
	for _, sql := range good {
		t.Run(sql, func(t *testing.T) {
			if err := Check(sql); err != nil {
				t.Errorf("应放行却拒了: %v", err)
			}
		})
	}
}

func TestCheckRejects(t *testing.T) {
	bad := []struct{ name, sql string }{
		{"裸 DML", "delete from t"},
		{"前导空白的 DML", "\n\t  delete from t"},
		{"注释藏 DML", "/*x*/delete from t"},
		{"行注释藏 DML", "--x\ndelete from t"},
		{"块注释分开关键词", "/*x*/ /*y*/ drop table t"},
		{"WITH 后面接 DELETE", "with x as (select 1 from dual) delete from t where 1=1"},
		{"多语句", "select 1; drop table t"},
		{"两条查询", "select 1; select 2 from dual"},
		{"host 命令", "host id"},
		{"! 命令", "!id"},
		{"@ 执行脚本", "@/tmp/x.sql"},
		{"@@ 执行脚本", "@@x"},
		{"start 执行脚本", "start x.sql"},
		{"spool 写文件", "spool /tmp/x"},
		{"connect 换账号", "connect ds/ds@//h:1521/s"},
		{"exit", "exit"},
		{"单斜杠", "select 1 from dual /"},
		{"FOR UPDATE", "select * from t for update"},
		{"FOR SHARE", "select * from t FOR SHARE"},
		{"SELECT INTO", "select a into :x from t"},
		{"PL/SQL 块", "begin p; end;"},
		{"DECLARE", "declare x number;"},
		{"CALL", "call p()"},
		{"EXECUTE", "execute immediate 'drop table t'"},
		{"COMMIT", "select 1 from dual commit"},
		{"LOCK", "lock table t in exclusive mode"},
		{"COPY", "copy t from program 'id'"},
		{"NEXTVAL", "select nextval('s') from dual"},
		{"SETVAL", "select setval('s',1) from dual"},
		{"TRUNCATE", "truncate table t"},
		{"MERGE", "merge into t using u on (1=1) when matched then update set a=1"},
		// 受限包:dbms_lob 是刻意整包禁的(同包里有 write/erase),见 safesql.go 的注释
		{"dbms_lob.substr", "select dbms_lob.substr(a,10,1) from dual"},
		{"schema 限定的 dbms_lob", "select sys.dbms_lob.substr(a,1,1) from dual"},
		{"utl_http", "select utl_http.request('http://x') from dual"},
		{"utl_file", "select utl_file.fopen('/tmp','a','r') from dual"},
		{"PG 读文件", "select pg_read_file('/etc/passwd')"},
		{"PG 关只读", "select set_config('default_transaction_read_only','off',false)"},
		{"dblink", "select dblink_exec('c','drop table t')"},
		// 特权视图
		{"dba_users", "select * from dba_users"},
		{"v$session", "select * from v$session"},
		{"gv$instance", "select * from gv$instance"},
		{"x$ 内部表", "select * from x$kzsrt"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			err := Check(c.sql)
			if err == nil {
				t.Fatalf("应拒绝却放行了: %s", c.sql)
			}
			// 拒绝理由要能看:不能是空消息
			if strings.TrimSpace(err.Error()) == "" {
				t.Error("拒绝时必须给出理由")
			}
		})
	}
}

func TestCheckRejectsEmptyAndLong(t *testing.T) {
	if err := Check(""); err == nil {
		t.Error("空语句应被拒")
	}
	if err := Check("   \n\t  "); err == nil {
		t.Error("全空白应被拒")
	}
	if err := Check("select 1 from dual where a='" + strings.Repeat("x", MaxSQLLen) + "'"); err == nil {
		t.Error("超长语句应被拒")
	}
	if err := Check("select 1 from dual\x00"); err == nil {
		t.Error("含空字符应被拒")
	}
}

// 误杀检查:上面那组"该放行"里全是真实会写出来的查询。这里再单独钉几条最容易踩的。
func TestCheckDoesNotOverblock(t *testing.T) {
	mustPass := []string{
		"select max(a), min(b), count(distinct c) from t group by d having count(*)>1",
		"select a from t where b in (select c from u)",
		"select case when a=1 then 'x' else 'y' end from t",
		"select a||b||'|'||c from t",
		"select to_char(d,'yyyy-mm-dd') from t",
		"select * from t where d between to_date('2026-01-01','yyyy-mm-dd') and sysdate",
		"select a from t order by 1 fetch first 10 rows only",
		"select a from t where b like '%设置%'",
	}
	for _, sql := range mustPass {
		t.Run(sql, func(t *testing.T) {
			if err := Check(sql); err != nil {
				t.Errorf("这条是常用查询,不该被拒: %v\n  %s", err, sql)
			}
		})
	}
}

// Clean 与 Normalize 的分工:Clean 是"拿去执行"的形态,**字面量里的值必须原样保留**;
// Normalize 是"拿来扫描"的形态,字面量内容掏空。两者搞混过一次 ——
// WrapRowLimit 当初用了 Normalize,于是 `where b='x'` 被拼成了 `where b=”`,条件值直接丢了。
func TestCleanKeepsLiteralValues(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"字面量原样保留", `select a from t where b='x'`, `select a from t where b='x'`},
		{"字面量里的空格保留", `select a from t where b='x  y'`, `select a from t where b='x  y'`},
		{"字面量里的分号保留", `select 'a;b' from dual`, `select 'a;b' from dual`},
		{"字面量里的注释符保留", `select 'a--b' from dual`, `select 'a--b' from dual`},
		{"'' 转义保留", `select 'it''s' from dual`, `select 'it''s' from dual`},
		{"双引号标识符保留", `select "MY COL" from t`, `select "MY COL" from t`},
		{"q 引号保留", `select q'[x--y]' from dual`, `select q'[x--y]' from dual`},
		{"注释仍要剥", "select a /*c*/ from t", "select a from t"},
		{"空白仍要折", "select  a\n\tfrom   t", "select a from t"},
		{"尾分号仍要剥", "select a from t;", "select a from t"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Clean(c.in); got != c.want {
				t.Errorf("Clean(%q)\n 得到 %q\n 期望 %q", c.in, got, c.want)
			}
		})
	}
}

func TestWrapRowLimit(t *testing.T) {
	// 普通 SELECT:包装,且内层语句逐字保留(别在包装时顺手改了语义)
	got, wrapped := WrapRowLimit("oracle", "select a from t where b=1")
	if !wrapped {
		t.Fatal("普通 SELECT 应当被包装")
	}
	if !strings.Contains(got, "select a from t where b=1") {
		t.Errorf("内层语句被改动了: %s", got)
	}
	if !strings.Contains(got, "ROWNUM <=") {
		t.Errorf("Oracle 分支应当用 ROWNUM: %s", got)
	}
	kb, wrapped := WrapRowLimit("kingbase", "select a from t")
	if !wrapped || !strings.Contains(kb, "LIMIT") {
		t.Errorf("金仓分支应当用 LIMIT: %s (wrapped=%v)", kb, wrapped)
	}

	// WITH 开头:Oracle 11g 不支持子查询里的 WITH,不包装(调用方要据此标注"未在库侧限流")
	if _, wrapped := WrapRowLimit("oracle", "with c as (select 1 from dual) select * from c"); wrapped {
		t.Error("WITH 开头不该被包装")
	}
	// 自带行限制:不必再套
	for _, s := range []string{"select a from t fetch first 10 rows only", "select a from t limit 10"} {
		if _, w := WrapRowLimit("kingbase", s); w {
			t.Errorf("已自带行限制,不该再包: %s", s)
		}
	}
	// 尾分号与注释:先规范化再包,别把分号带进子查询
	got, _ = WrapRowLimit("oracle", "select a from t; -- 说明")
	if strings.Contains(got, ";") {
		t.Errorf("包装后不该残留分号: %s", got)
	}
	if !strings.Contains(got, "select a from t") {
		t.Errorf("语句本体丢了: %s", got)
	}
}
