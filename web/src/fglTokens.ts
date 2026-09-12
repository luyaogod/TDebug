// 4GL(BDL)语法高亮:移植自 BDL 扩展(D:\我的项目\BDL,同作者,MIT)的
// syntaxes/bdl.tmLanguage.json —— 只发出三类 token:comment(绿)/ string(棕)/ keyword(蓝),
// 其余(数字、标识符、运算符、函数名)一律继承主题默认前景色,靠「省略规则」实现而非覆盖;
// 类型关键字也在同一张关键字表里,所以统一是蓝色(不再单列 type 配色)。
//
// TextMate 语法文件 vs Monaco/Monarch 的三处引擎差异(均已在真实 Monarch 词法器上实测):
//   1. Monarch 没有 scope 栈。TextMate 里「无名内部规则」靠外层 scope 继承颜色,
//      这里必须在字符串状态内显式重申 'string'(转义符、成对引号),否则转义符会掉成默认色。
//   2. Monarch 把规则正则对「剩余文本」求值、`^` 锚在当前列 —— BDL 关键字的
//      lookbehind `(?<![A-Za-z0-9_])` 因此失效(实测 xEND / 2END / _END 都会把 END 误染蓝;
//      真实源码里 g_append、l_select 就是这批误报)。故在关键字规则之后补一条中性词符规则
//      `[0-9A-Za-z_]+`,整段吃掉标识符与数字词,镶嵌在词尾的关键字就再没机会被匹配。
//      lookahead 仍然有效,所以 END2 / END_ 依旧不染色,END定义 依旧染色 ——
//      与 BDL「用 ASCII 前后瞻而非 \b」的用意一致(\b 认中日韩汉字会静默失配)。
//   3. `ON ACTION <动作名>` 中和规则必须写成「铺满式捕获」:Monarch 要求 actions 与捕获组
//      1:1 且各组无缝铺满整条匹配,照搬 TextMate 的 `(?i:(ON)[ \t]+(ACTION))…`(其中
//      `(?i:…)` 是非捕获组)会在词法期抛 matched number of groups does not match。

/**
 * BDL 关键字表:逐字照抄 BDL `syntaxes/bdl.tmLanguage.json` 的 keyword.bdl match(1966 字符)。
 * 前后瞻用 ASCII 字符类而非 \b(见上文第 2 点);词边界判定靠它 + 中性词符规则共同完成。
 */
export const FGL_KEYWORD_RE =
  /(?<![A-Za-z0-9_])(?i:ABSOLUTE|ACCEPT|ACTION|AFTER|ALL|ALTER|AND|ANY|APPEND|ARRAY|AS|ASC|ATTRIBUTE|ATTRIBUTES|AUTHORIZATION|AVG|BEFORE|BEGIN|BETWEEN|BIGINT|BIGSERIAL|BOOLEAN|BOTTOM|BREAKPOINT|BUFFER|BY|BYTE|CALL|CANCEL|CASE|CATCH|CENTER|CHANGE|CHAR|CIRCUIT|CLEAR|CLIPPED|CLOSE|COLLAPSE|COMMAND|COMMIT|CONNECT|CONSTANT|CONSTRAINT|CONSTRUCT|CONTINUE|COUNT|CREATE|CROSS|CURRENT|CURSOR|DATABASE|DATE|DATETIME|DAY|DECIMAL|DECLARE|DEFAULT|DEFAULTS|DEFER|DEFINE|DELETE|DESC|DIALOG|DISCONNECT|DISPLAY|DISTINCT|DO|DOUBLE|DROP|DYNAMIC|ELSE|ELSIF|END|ERROR|ESCAPE|EVERY|EXCLUSIVE|EXECUTE|EXISTS|EXIT|EXPAND|EXPLAIN|EXTERNAL|FALSE|FETCH|FGL|FIELD|FILE|FINISH|FIRST|FLOAT|FLUSH|FOR|FOREACH|FOREIGN|FORM|FORMAT|FORMONLY|FOUND|FRACTION|FREE|FROM|FULL|FUNCTION|GLOBALS|GOTO|GRANT|GROUP|HAVING|HEADER|HELP|HIDE|HOLD|HOUR|IF|IMMEDIATE|IMPORT|IN|INDEX|INFIELD|INITIALIZE|INNER|INOUT|INPUT|INSERT|INT|INTEGER|INTERFACE|INTERRUPT|INTERSECT|INTERVAL|INTO|IS|ISOLATION|JAVA|JOIN|KEY|LABEL|LAST|LEFT|LENGTH|LET|LEVEL|LIKE|LINE|LINENO|LOAD|LOCK|MAIN|MARGIN|MATCHES|MAX|MENU|MESSAGE|MIN|MINUS|MINUTE|MOD|MODE|MODULE|MONEY|MONLY|MONTH|NAME|NATURAL|NEED|NEXT|NO|NOT|NOTFOUND|NOWAIT|NULL|NUMERIC|OF|ON|OPEN|OPTION|OPTIONS|OR|ORDER|OTHERWISE|OUT|OUTER|PACKAGE|PAGE|PAGENO|PAUSE|PERCENT|PIPE|PREPARE|PRIMARY|PRINT|PRINTER|PRINTX|PRIOR|PRIVATE|PROMPT|PUBLIC|PUT|QUIT|RAISE|REAL|RECORD|RECOVER|REFERENCES|RELATIVE|RELEASE|RENAME|REPORT|RETURN|RETURNING|REVOKE|RIGHT|ROLLBACK|ROW|SAVEPOINT|SCHEMA|SCREEN|SCROLL|SECOND|SELECT|SELECTION|SEQUENCE|SERIAL|SERIAL8|SESSION|SET|SHARE|SHORT|SHOW|SKIP|SLEEP|SMALLFLOAT|SMALLINT|SPACES|SQL|SQLERROR|START|STATIC|STATISTICS|STEP|STOP|STRING|STYLE|STYLES|SUBDIALOG|SUM|SYNONYM|TABLE|TEMP|TEMPORARY|TERMINATE|TEXT|THEN|THROUGH|THRU|TIMER|TINYINT|TO|TOP|TRAILER|TRIGGER|TRUE|TRY|TYPE|UNBUFFERED|UNION|UNIQUE|UNITS|UNLOAD|UPDATE|USING|VALIDATE|VALUES|VARCHAR|VIEW|WARNING|WEEKDAY|WHEN|WHENEVER|WHERE|WHILE|WINDOW|WITH|WITHOUT|WORDWRAP|WORK|WRAP|YEAR)(?![A-Za-z0-9_])/

/**
 * Monaco Monarch 语言定义(供 `monaco.languages.setMonarchTokensProvider('4gl', …)`)。
 *
 * 规则顺序即优先级,不可随意调整:
 *   注释 → 字符串 → 中和规则 → 关键字 → 中性词符 → 兜底
 * 其中「中和规则必须排在关键字之前」与 TextMate 一致(TextMate 里被消费的文本不会再被
 * 后续规则看到,Monarch 里则是首个匹配即生效)。
 *
 * 类型:空 token('')与捕获数组不满足 monaco 的严格 IMonarchLanguage 类型,故 as any
 * (与移植前 SourceView 里的写法一致);语言本身是纯数据,不 import monaco,便于测试直接引用。
 */
export const FGL_MONARCH = {
  // 对齐 BDL 关键字规则的 (?i)(大小写不敏感);注释/字符串/中和规则无字母,不受影响
  ignoreCase: true,
  tokenizer: {
    root: [
      // ---- 注释(必须排在字符串之前:实测注释里含引号远多于字符串里含 #)----
      // --#{ 必须排在 --# 与 -- 之前
      [/--#\{/, { token: 'comment', next: '@cmt' }],
      [/--#.*$/, 'comment'],
      // `--` 不能行首锚定:真实源码有行尾 -- 注释
      [/--.*$/, 'comment'],
      [/#.*$/, 'comment'],
      // 花括号注释按语言规定不能嵌套:遇第一个 } 即结束(故内容两份一起抹掉)
      [/\{/, { token: 'comment', next: '@brace' }],

      // ---- 字符串(在注释之后;%" 本地化串必须排在 " 之前)----
      [/%"/, { token: 'string', next: '@loc' }],
      [/'/, { token: 'string', next: '@sq' }],
      [/"/, { token: 'string', next: '@dq' }],
      [/`/, { token: 'string', next: '@bt' }],

      // ---- 中和规则(无名 = 保持默认色;必须排在关键字之前)----
      // 点号后的成员/方法访问:g_browser.clear()、g_qryparam.where
      [/\.[ \t]*[A-Za-z_][A-Za-z0-9_]*/, ''],
      // ON ACTION 后的动作名是用户自定义的(ON ACTION next/close/delete),只给 ON/ACTION 上色。
      // 捕获组必须无缝铺满整条匹配,故把空白也各列一组(见文件头第 3 点)。
      [
        /(ON)([ \t]+)(ACTION)([ \t]+)([A-Za-z_][A-Za-z0-9_]*)/,
        ['keyword', '', 'keyword', '', ''],
      ],
      // 预处理器指令不是关键字:&ifdef / &else / &endif / &include
      [/&[A-Za-z_][A-Za-z0-9_]*/, ''],

      // ---- 全部关键字共用一个 keyword token(类型词也在表内 → 统一蓝)----
      [FGL_KEYWORD_RE, 'keyword'],

      // ---- 中性词符:整段吃掉标识符/数字词,防词尾镶嵌的关键字被上一条匹配 ----
      [/[0-9A-Za-z_]+/, ''],
      // 其余一律不上色(数字、运算符、括号、中文……)
      [/./, ''],
    ],
    // --#{ … --#}
    cmt: [
      [/--#\}/, { token: 'comment', next: '@pop' }],
      [/./, 'comment'],
    ],
    // { … }(不可嵌套:遇第一个 } 结束)
    brace: [
      [/\}/, { token: 'comment', next: '@pop' }],
      [/./, 'comment'],
    ],
    // %"…" 本地化字符串:内部规则必须显式重申 string(Monarch 无 scope 栈)
    loc: [
      [/\\../, 'string'],
      [/""/, 'string'],
      [/"/, { token: 'string', next: '@pop' }],
      [/./, 'string'],
    ],
    // '…':内部 '' 是字面引号,故闭合引号判定为「后面不再跟一个 '」
    sq: [
      [/\\../, 'string'],
      [/''/, 'string'],
      [/'/, { token: 'string', next: '@pop' }],
      [/./, 'string'],
    ],
    // "…":同上,内部 "" 是字面引号
    dq: [
      [/\\../, 'string'],
      [/""/, 'string'],
      [/"/, { token: 'string', next: '@pop' }],
      [/./, 'string'],
    ],
    // 反引号是原始字符串:不处理转义
    bt: [
      [/`/, { token: 'string', next: '@pop' }],
      [/./, 'string'],
    ],
  },
} as any
