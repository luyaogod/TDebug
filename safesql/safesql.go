// Package safesql 只读 SQL 的文本层防线。
//
// 它是**防线之一,不是全部**,别把它当成"过了就绝对安全":
//   - 真正的兜底是库侧的只读事务(SET TRANSACTION READ ONLY);
//   - "数据不拼进命令行"由 host.OutputStdin 保证;
//   - 这里解决的是"这条语句看起来像不像只读"。
//
// 它是**文本扫描,不是 SQL 解析器**。有两件事它原则上看不出来:
//  1. 自治事务(PRAGMA AUTONOMOUS_TRANSACTION):一个看着是 SELECT 的函数调用
//     可以真的写库并提交 —— 只读事务也拦不住它;
//  2. 工作量:限行数不等于限扫描量。
//
// 这两条要如实写进文档,不要粉饰。
package safesql

import (
	"fmt"
	"strings"
	"unicode"
)

// 上限常量集中在这里,让"能提交多大的语句/能拿回多少结果"只有一处定义。
const (
	MaxSQLLen    = 8192  // 单条语句字节上限
	MaxRows      = 200   // 回给调用方的行数上限
	MaxResult    = 32768 // 回给调用方的字节上限
	MaxValue     = 4096  // 单个字段值的字节上限(一个 CLOB 就是几 MB)
	MaxSpillSize = 8 << 20
	MaxSpillRows = 50000
)

// ---------- 清理与规范化 ----------

// Clean 把语句整理成"可以直接执行"的形态:剥掉注释、把**字面量之外**的连续空白
// 折成单个空格、去掉唯一的末尾分号。字面量(含其中的空格)原样保留。
//
// 送进 sqlplus 的应当是它:折成单行之后,"换行 + 一条 sqlplus 命令"这类构造就消失了
// (单独一行的 `/` 是 sqlplus 的"重跑上一条",不折行就还活着)。
func Clean(sql string) string { return scan(sql, false) }

// Normalize 在 Clean 的基础上,把**字面量的内容掏空**(`'abc'` → `”`)。
// 只用于扫描判定:这样 `select 'delete from t' from dual` 不会因为串里的关键词被误判。
//
// 注意它**不能拿去执行** —— 字面量里的值会被丢掉。
func Normalize(sql string) string { return scan(sql, true) }

// scan 共用的扫描器;blankLiterals 决定字面量内容是保留还是掏空。
func scan(sql string, blankLiterals bool) string {
	sql = strings.TrimPrefix(sql, "\ufeff")
	rs := []rune(sql)
	n := len(rs)
	var b strings.Builder

	// 空白与剥掉的注释都只"记一个待写的空格":连续出现时不会写出多个,
	// 夹在词之间时也不会把 a/*x*/b 粘成一个词。
	needSpace := false
	emit := func(r rune) {
		if needSpace {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			needSpace = false
		}
		b.WriteRune(r)
	}
	markSpace := func() { needSpace = true }

	for i := 0; i < n; {
		c := rs[i]
		switch {
		case c == '\'' || c == '"':
			emit(c)
			i++
			for i < n {
				if rs[i] == c {
					if i+1 < n && rs[i+1] == c { // 连写两个 = 转义,串没结束
						if !blankLiterals {
							b.WriteRune(c)
							b.WriteRune(c)
						}
						i += 2
						continue
					}
					i++
					break
				}
				if !blankLiterals {
					b.WriteRune(rs[i])
				}
				i++
			}
			emit(c)
		case (c == 'q' || c == 'Q') && i+2 < n && rs[i+1] == '\'':
			// Oracle 的 q'<分隔符>…<分隔符>' 替代引号
			delim := rs[i+2]
			closer := delim
			switch delim {
			case '[':
				closer = ']'
			case '(':
				closer = ')'
			case '{':
				closer = '}'
			case '<':
				closer = '>'
			}
			if !blankLiterals {
				emit(c) // 用 emit 才会把前面攒着的空格先补上,否则会与上一个词粘住
				b.WriteRune('\'')
				b.WriteRune(delim)
			} else {
				// 掏空也要留个空串,否则相邻词会粘在一起
				emit('\'')
				emit('\'')
			}
			i += 3
			for i < n {
				if rs[i] == closer && i+1 < n && rs[i+1] == '\'' {
					if !blankLiterals {
						b.WriteRune(closer)
						b.WriteRune('\'')
					}
					i += 2
					break
				}
				if !blankLiterals {
					b.WriteRune(rs[i])
				}
				i++
			}
		case c == '-' && i+1 < n && rs[i+1] == '-':
			for i < n && rs[i] != '\n' {
				i++
			}
			markSpace()
		case c == '/' && i+1 < n && rs[i+1] == '*':
			i += 2
			for i+1 < n && !(rs[i] == '*' && rs[i+1] == '/') {
				i++
			}
			if i+1 < n {
				i += 2
			} else {
				i = n
			}
			markSpace()
		case unicode.IsSpace(c):
			for i < n && unicode.IsSpace(rs[i]) {
				i++
			}
			markSpace()
		default:
			emit(c)
			i++
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimSuffix(out, ";")
	return strings.TrimSpace(out)
}

// firstWord 取首个词(大写);取不到返回空串
func firstWord(s string) string {
	i := strings.IndexAny(s, " \t(")
	if i < 0 {
		return strings.ToUpper(s)
	}
	return strings.ToUpper(s[:i])
}

// ---------- 检查 ----------

// 词边界黑名单:命中即拒。每条都写清挡的是什么 —— 这些不是洁癖,是只读防线的必要条件。
var bannedWords = []struct{ word, why string }{
	{"INSERT", "写数据"}, {"UPDATE", "写数据"}, {"DELETE", "写数据"},
	{"MERGE", "写数据(也是 WITH … DELETE 的入口)"}, {"UPSERT", "写数据"},
	{"TRUNCATE", "清表"}, {"DROP", "删对象"},
	{"ALTER", "改结构——DDL 会隐式提交,直接终结只读事务"},
	{"CREATE", "建对象——同上会终结只读事务"}, {"RENAME", "改结构"},
	{"COMMENT", "写元数据"}, {"GRANT", "改权限"}, {"REVOKE", "改权限"},
	{"AUDIT", "改审计"}, {"NOAUDIT", "改审计"},
	{"PURGE", "Oracle 破坏性语句"}, {"FLASHBACK", "Oracle 破坏性语句"},
	{"BEGIN", "PL/SQL 块:自治事务不受调用方只读事务约束"},
	{"DECLARE", "PL/SQL 块"}, {"CALL", "调用过程:过程内可提交、可自治"},
	{"DO", "PG 的匿名代码块"},
	{"EXECUTE", "动态 SQL"}, {"EXEC", "动态 SQL"},
	{"COMMIT", "会终结只读事务,其后语句恢复读写"},
	{"ROLLBACK", "事务控制"}, {"SAVEPOINT", "事务控制"},
	{"INTO", "SELECT … INTO 是 PL/SQL 的写路径;PG 的 SELECT INTO 会建表"},
	{"LOCK", "锁表"}, {"COPY", "PG 的 COPY … FROM 写表、COPY … TO PROGRAM 是 RCE"},
	{"VACUUM", "PG 维护"}, {"ANALYZE", "PG 维护"}, {"REINDEX", "PG 维护"},
	{"CLUSTER", "PG 维护"}, {"REFRESH", "物化视图写"},
	{"NOTIFY", "PG 侧信道"}, {"LISTEN", "PG 侧信道"}, {"UNLISTEN", "PG 侧信道"},
	{"NEXTVAL", "推进序列——看着只读,实际写"}, {"SETVAL", "改序列"},
	{"SET", "改会话/GUC(sqlplus 侧也能被它改掉)"},
}

// 包/函数前缀黑名单。命中即拒。
//
// 为什么连 dbms_lob 一起禁:同包里就有 write/append/erase/trim 这些写函数,
// 允许这个前缀,文本扫描就无法区分 substr 与 write —— 除非维护一份精确到函数名的
// 白名单,那是永久的维护负担与漏网温床。而我们自己那条 dbms_lob.substr 是**代码
// 字面量**,根本不走这条白名单,禁掉对功能零影响。
var bannedPrefixes = []string{
	"utl_",         // utl_file 读写服务器文件、utl_http/smtp/tcp 出网外带、utl_inaddr 探测
	"dbms_",        // dbms_scheduler 起 OS 作业、dbms_pipe/lock/alert、dbms_java、dbms_sql、dbms_lob…
	"owa_",         // Oracle Web 工具包
	"htp.", "htf.", // Oracle Web 工具包
	"pg_",             // pg_read_file/pg_ls_dir 读服务器文件、pg_terminate_backend 杀会话、pg_sleep
	"dblink",          // dblink/dblink_exec 远程执行
	"lo_",             // lo_import/lo_export 大对象读写
	"set_config",      // 能在一条 SELECT 里把 default_transaction_read_only 改回 off,直接拆掉只读事务
	"current_setting", // 会话信息面
}

// 特权视图前缀:跨 schema / 跨会话的元数据面(dba_users 有口令哈希、v$sqltext 能看到别人的 SQL)
var bannedViews = []string{"dba_", "v$", "gv$", "x$", "wri$_"}

// 语句首词命中即拒的 sqlplus 客户端命令。
// 它们不是 SQL,是"对工具下命令"—— 其中 @/start 能执行磁盘上任意脚本(绕过整个白名单),
// host/! 直接在服务器上执行 shell 命令。
var bannedLeadingWords = []string{
	"@", "@@", "START", "EDIT", "HOST", "!", "SPOOL", "RUN", "SAVE", "GET", "STORE",
	"INPUT", "DEL", "REMARK", "DEFINE", "UNDEFINE", "ACCEPT", "PROMPT", "VARIABLE",
	"PRINT", "EXEC", "EXECUTE", "SET", "COLUMN", "COL", "WHENEVER", "PAUSE",
	"CONNECT", "CONN", "DISCONNECT", "DISC", "EXIT", "QUIT", "SHUTDOWN", "STARTUP", "RECOVER", "COPY",
}

// Check 判定一条语句是否允许只读执行;返回 nil 才放行。
//
// 拒绝时**明确报错**并点名是哪条规则 —— 这里刻意不像筛选条件那样静默退化成空集:
// 静默退化成 0 行会被读成"这条业务数据不存在",那是最危险的错误结论。
func Check(sql string) error {
	if strings.ContainsRune(sql, 0) {
		return fmt.Errorf("语句含空字符")
	}
	if len(sql) > MaxSQLLen {
		return fmt.Errorf("语句过长(上限 %d 字节,实际 %d)", MaxSQLLen, len(sql))
	}
	code := Normalize(sql)
	if code == "" {
		return fmt.Errorf("语句为空")
	}
	if strings.Contains(code, ";") {
		return fmt.Errorf("只允许**单条**语句(语句中间出现了分号)—— 一次只想一件事")
	}
	head := firstWord(code)
	switch head {
	case "SELECT", "WITH":
	case "/":
		return fmt.Errorf("`/` 是 sqlplus 的「重跑上一条」命令,不允许")
	default:
		for _, w := range bannedLeadingWords {
			if head == w {
				return fmt.Errorf("%q 是 sqlplus 客户端的命令,不是查询语句,不允许", head)
			}
		}
		return fmt.Errorf("只允许 SELECT 或 WITH 开头的查询(实际以 %q 开头)", head)
	}
	upper := strings.ToUpper(code)
	// 单独的 "/" 作为末尾一个词,也是 sqlplus 的重跑命令
	if fields := strings.Fields(upper); len(fields) > 0 && fields[len(fields)-1] == "/" {
		return fmt.Errorf("末尾的 `/` 是 sqlplus 的「重跑上一条」命令,不允许")
	}
	for _, bw := range bannedWords {
		if containsWord(upper, bw.word) {
			return fmt.Errorf("语句里出现了 %s —— %s;只读查询不允许", bw.word, bw.why)
		}
	}
	// 行锁短语。单独禁 FOR 会误伤,所以按两词短语匹配。
	// 只读事务本来也会挡它(Oracle ORA-01456),这里挡是为了给出比库报错更清楚的拒绝理由。
	for _, ph := range []string{" FOR UPDATE", " FOR SHARE", " FOR NO KEY UPDATE", " FOR KEY SHARE"} {
		if strings.Contains(upper, ph) {
			return fmt.Errorf("语句里出现了%s —— 那是行锁,不是只读查询", strings.TrimSpace(ph))
		}
	}
	for _, p := range bannedPrefixes {
		if containsPrefix(upper, strings.ToUpper(p)) {
			return fmt.Errorf("用到了受限的包/函数 %s* —— 它们带副作用或可读写服务器资源;只读查询不允许", p)
		}
	}
	for _, v := range bannedViews {
		if containsPrefix(upper, strings.ToUpper(v)) {
			return fmt.Errorf("访问了特权视图 %s*(跨 schema/跨会话的元数据);只查业务表请用 all_*/user_* 开头的视图", v)
		}
	}
	return nil
}

// containsWord 在**已大写**的文本里按词边界找词。
// 词字符 = 字母/数字/_/$/#;两侧都不是词字符才算命中(于是 DELETED 不会因为 DELETE 被误杀)。
func containsWord(upper, word string) bool {
	for i := 0; ; {
		j := strings.Index(upper[i:], word)
		if j < 0 {
			return false
		}
		j += i
		before := j == 0 || !isWordRune(rune(upper[j-1]))
		k := j + len(word)
		after := k >= len(upper) || !isWordRune(rune(upper[k]))
		if before && after {
			return true
		}
		i = k
	}
}

// containsPrefix 找"标识符以该前缀开头"的位置:前缀前面不能是词字符(避免 mypg_ 命中 pg_)
func containsPrefix(upper, prefix string) bool {
	for i := 0; ; {
		j := strings.Index(upper[i:], prefix)
		if j < 0 {
			return false
		}
		j += i
		if j == 0 || !isWordRune(rune(upper[j-1])) {
			return true
		}
		i = j + 1
	}
}

func isWordRune(r rune) bool {
	return r == '_' || r == '$' || r == '#' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// ---------- 限行包装 ----------

// WrapRowLimit 给语句套一层行数上限,返回 (可执行的语句, 是否包装了)。
//
// 不包装的情形要**如实告诉调用方**(第二个返回值为 false),由它显式标注
// "未在数据库侧限流" —— 否则使用者会以为"就这么多行":
//   - 语句以 WITH 开头:Oracle 11g 不支持子查询里的 WITH 子句(T100 环境常见);
//   - 已经自带 LIMIT / FETCH FIRST:不必再套。
//
// 注意:行数上限限的是**返回行数,不是工作量**。`count(*)` 与笛卡尔积照样能把库跑满。
func WrapRowLimit(dialect, sql string) (string, bool) {
	code := Clean(sql)
	if code == "" {
		return sql, false
	}
	if firstWord(code) == "WITH" {
		return sql, false
	}
	upper := strings.ToUpper(code)
	if strings.Contains(upper, " FETCH FIRST") || strings.Contains(upper, " FETCH NEXT") ||
		strings.Contains(upper, " LIMIT ") || strings.HasSuffix(upper, " LIMIT") {
		return sql, false
	}
	// 用 Clean 后的语句:注释已剥、尾分号已去,不会把分号带进子查询;
	// 而字面量内容是**保留**的(Normalize 会掏空字面量,那是给扫描用的,拿去执行会丢值)。
	switch dialect {
	case "kingbase":
		return fmt.Sprintf("SELECT * FROM (%s) _tdbg_cap LIMIT %d", code, MaxRows+1), true
	default: // oracle
		return fmt.Sprintf("SELECT * FROM (%s) WHERE ROWNUM <= %d", code, MaxRows+1), true
	}
}
