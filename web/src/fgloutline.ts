// 4GL(BDL)源码大纲解析:移植自 BDL 扩展(D:\我的项目\BDL,同作者,MIT)的 src/extension.ts。
// 原实现同时产出 DocumentSymbol(大纲)与 FoldingRange(折叠);本移植**只保留大纲**,
// 折叠相关的 FOLD_ONLY / scanFoldOnly / RE_FOLD_* 全部未移植(编辑器已 folding: false)。
//
// 结构:maskLines(掩码)→ scanBlocks(块栈扫描)→ fixup(范围修正)→ toOutlineNodes(转对外节点)。
// 设计要点(逐条都有真实语料实证,详见 BDL docs/DESIGN.md):
//   - 必须用块栈而不是缩进:真实源码的缩进会谎报层级(同类子块 ON ACTION 比宿主还浅)。
//   - 扫描前把字符串内容与三类注释(#、--、{ })抹成等长空格:否则跨行字符串里的 SQL 关键字、
//     块注释里的 ON ACTION、注释里的撇号(会开启跨行字符串状态)都会造幻影或吃掉成片节点。
//   - 块开头要按构造消歧:RECORD 里名叫 report/construct 的字段不是块开头。
//   - 深嵌套一律用显式栈,不用递归(畸形文件约 2000 层嵌套会让递归栈溢出、大纲全空)。
//
// 与 BDL 的一致性约定:标签文本、块范围、以及 BDL 已声明的已知偏差(见 fgl-fixtures 里
// *.expected.json 的 deviation 字段)都**照原样保留**,以便用同一批文档推导夹具 1:1 对拍。

export type OutlineKind =
  | 'FUNCTION' | 'MAIN' | 'REPORT' | 'DIALOG'
  | 'INPUT' | 'DISPLAY' | 'CONSTRUCT' | 'MENU'
  | 'SUB'

export interface OutlineNode {
  label: string
  kind: OutlineKind
  line: number // 1-based,磁盘源码行(调试页显示/跳转时需 + lineOffset)
  endLine: number // 1-based,块结束行(含);= 配对 END <TYPE> 所在行
  selStart: number // 0-based 名称起始列(selectionRange 语义,跳转落点)
  selEnd: number
  children?: OutlineNode[]
}

/* ============================ 词法表 ============================
 * 全部建为模块级常量:大 alternation 每次调用重建会多花 ~78ms。
 */

const W_S = '[ \\t]*'
const W_P = '[ \\t]+'
/** 限定名 / 数组记录前缀:g_pmdl_m.*、s_browse.*、type_t.chr50 */
const ID = '[A-Za-z_][A-Za-z0-9_.]*'
/** 词边界:关键字不得粘在更长的词上。用 ASCII 前瞻而非 \b。 */
const NB = '(?![A-Za-z0-9_])'

/* 注意:所有正则都只对「去掉行尾 \r」的副本求值。
 * 本文件常见 100% CRLF,而 JS 的 `.` 不匹配 \r —— 任何以 `.*$` 结尾的正则
 * 在原始行上会静默返回 0 个匹配。见 maskLines 里的 code/raw 两份数组。 */

/** END <TYPE>。故意不带 `$`。 */
const RE_END = new RegExp(
  '^' + W_S + 'END' + W_P + '(FUNCTION|MAIN|REPORT|DIALOG|INPUT|DISPLAY|CONSTRUCT|MENU)' + NB,
  'i'
)

/** `{ }` 是 BDL 的块注释(不可嵌套),与高亮规则保持一致。 */
const CH_LF = 10, CH_HASH = 35, CH_SQ = 39, CH_DQ = 34, CH_BT = 96, CH_BS = 92, CH_CR = 13
const CH_DASH = 45, CH_LB = 123, CH_RB = 125

/**
 * BDL 数据类型关键字。用来把 RECORD 里的字段声明与块开头区分开:
 * `construct INTEGER` 是「名为 construct 的字段,类型 INTEGER」,不是 CONSTRUCT 块。
 * 只在「名字后面什么都没有(行尾)」时才需要它来消歧。
 */
const TYPE_KEYWORDS =
  'INTEGER|INT|SMALLINT|BIGINT|TINYINT|SERIAL8|SERIAL|BIGSERIAL|' +
  'CHAR|VARCHAR|NVARCHAR|STRING|TEXT|DECIMAL|NUMERIC|MONEY|' +
  'FLOAT|SMALLFLOAT|DOUBLE|DATE|DATETIME|INTERVAL|BOOLEAN|BYTE|' +
  'DYNAMIC|STATIC|RECORD|LIKE|ARRAY'

/**
 * 子块开头:BEFORE ROW / AFTER FIELD x / ON ACTION controlp INFIELD f / ON CHANGE f
 *
 * 后接的关键字必须是**文档里列出的**事件/控制块,不能是「ON 后跟任意东西」——
 * 否则 SQL 的 JOIN 条件只要被格式化到行首就会被当成 dialog 子块:
 *   `ON t1.col_a = t2.col_a AND t1.col_b = t2.col_b`
 */
const SUB_EVENT =
  'ACTION|CHANGE|KEY|IDLE|TIMER|ROW' + W_P + 'CHANGE|EVERY' + W_P + 'ROW|LAST' + W_P + 'ROW|' +
  'FILL' + W_P + 'BUFFER|SORT|APPEND|INSERT|UPDATE|DELETE|EXPAND|COLLAPSE|' +
  'SELECTION' + W_P + 'CHANGE|DRAG_START|DRAG_FINISHED|DRAG_ENTER|DRAG_OVER|DROP'
const SUB_CONTROL =
  'INPUT|CONSTRUCT|DISPLAY|DIALOG|MENU|ROW|FIELD|INSERT|DELETE|UPDATE|GROUP' + W_P + 'OF'
/** REPORT 的 FORMAT 段控制块不属于 `ON …` / `BEFORE|AFTER …` 形状,得单列一支。 */
const SUB_REPORT = '(?:FIRST' + W_P + ')?PAGE' + W_P + '(?:HEADER|TRAILER)'
/**
 * `COMMAND [KEY (…)] option` 是 MENU 的 menu-option,与 `ON ACTION` **平级**,
 * 在 DIALOG 里也是 dialog-control-block。不认它,前面的 `BEFORE MENU` 会一路吞到
 * 下一个被认出的子块,把整段菜单项包进自己的范围里。
 */
const SUB_COMMAND = '(?:COMMAND)(?:' + W_P + 'KEY' + W_S + '\\([^)]*\\))?' + NB
const SUB_HEAD =
  '(?:BEFORE|AFTER)' + W_P + '(?:' + SUB_CONTROL + ')' + NB +
  '|(?:ON)' + W_P + '(?:' + SUB_EVENT + ')' + NB +
  '|' + SUB_REPORT + NB +
  '|' + SUB_COMMAND
const RE_SUB = new RegExp('^' + W_S + '(?<head>' + SUB_HEAD + ')(?<rest>.*)', 'i')

/** 危险点:`INPUT NO WRAP` / `INPUT WRAP` 是 OPTIONS 子句,不是 dialog 块,
 *  且它落点和真正的 INPUT 同一个缩进层级。整行精确匹配,避免把
 *  「记录名恰好以 NO 开头」的真 INPUT 误跳。 */
const RE_INPUT_OPTION = new RegExp('^' + W_S + 'INPUT' + W_P + '(?:NO' + W_P + ')?WRAP' + W_S + '$', 'i')

/** 全部块开头。备选项顺序有意义:带参数的形式必须排在裸形式之前。 */
const RE_OPEN = new RegExp(
  '^' + W_S + '(?:(?:PUBLIC|PRIVATE)' + W_P + ')?(?:' +
    '(?<fn>FUNCTION)' + W_S + '(?:[(][^)]*[)]' + W_S + ')?(?<fnName>' + ID + ')' + W_S + '[(]' +
    '|(?<main>MAIN)' + NB +
    // REPORT 必须带 name( —— 否则 RECORD 里名叫 `report` 的字段会被当成块开头:
    // 声明 `report DYNAMIC ARRAY OF …` 的字段会造出幻影 Module 节点;
    // 又因 REPORT 属 MODULE_KINDS,还会连带截断宿主函数(实测有一处让所在函数少掉 200 余行)。
    // 语料里 2,368 处 END REPORT 对应的声明 100% 是 `REPORT name(...)`。
    '|(?<report>REPORT)' + NB + W_P + '(?<reportName>' + ID + ')' + W_S + '[(]' +
    // DIALOG:`(?![A-Za-z0-9_.])` 挡掉 DIALOG.getCurrentRow / ui.DIALOG / FGL_DIALOG_*
    // 顺序有意义:ATTRIBUTES 必须排在带名形式之前,否则 `DIALOG ATTRIBUTES(...)`
    // 会把 ATTRIBUTES 当成声明式 dialog 的名字。单复数都要挡。
    '|(?<dialog>DIALOG)(?![A-Za-z0-9_.])(?:' + W_P + 'ATTRIBUTES?' + NB +
        '|' + W_P + '(?<dialogName>' + ID + ')' + W_S + '[(]' +
        '|' + W_S + '$)' +
    // INPUT:自带 (?!…WRAP…) 守卫,使 `INPUT NO WRAP` 落到「不匹配」而非把 NO 当记录名
    '|(?<input>INPUT)' + NB + '(?!' + W_P + '(?:NO' + W_P + ')?WRAP' + NB + ')' + W_P +
        '(?:(?<inputArray>ARRAY' + W_P + ID + ')' +
        '|(?<inputByName>BY' + W_P + 'NAME' + W_P + ID + ')' +
        '|(?<inputRecord>' + ID + '))' + NB +
    '|(?<display>DISPLAY)' + NB + W_P + 'ARRAY' + NB + W_P + '(?<displayArray>' + ID + ')' +
    // CONSTRUCT 后面跟 ON / FROM / ATTRIBUTE(S) 或行尾。
    // 不能只允许同行 ON —— 语料里 31 处真实块把 `ON …` 写在下一行(多行声明)。
    // 行尾那一支是 RECORD 字段误判(`construct LIKE …`)的来源,
    // 用「名字不得是类型关键字」把它堵住,而不是砍掉行尾分支。
    '|(?<construct>CONSTRUCT)' + NB + W_P + '(?<constructBy>BY' + W_P + 'NAME' + W_P + ')?' +
        '(?!(?:' + TYPE_KEYWORDS + ')' + NB + ')(?<constructName>' + ID + ')' +
        '(?=' + W_S + '(?:(?:ON|FROM|ATTRIBUTE|ATTRIBUTES)' + NB + '|$))' +
    // MENU 的标题可以是双引号、单引号、反引号或不加引号的标识符;漏了单引号会让
    // `MENU 'About' ATTRIBUTE(…)` 这种写法让整个 MENU 节点消失。
    '|(?<menu>MENU)(?![A-Za-z0-9_.])' + W_P + '(?<menuText>"[^"]*"|\'[^\']*\'|`[^`]*`' +
        '|(?!(?:ATTRIBUTES?)' + NB + ')' + ID + ')' +
    // 无标题 MENU 直接跟属性表:`MENU ATTRIBUTE (STYLE="dialog", …)`。必须单列一支 ——
    // 若只把 ATTRIBUTE 排除在标题候选之外,会让整条 menu 分支匹配失败而节点整个消失。
    '|(?<menuAttr>MENU)(?![A-Za-z0-9_.])' + W_P + 'ATTRIBUTES?' + NB +
    '|(?<inputBare>INPUT)' + NB + W_S + '$' +
    '|(?<menuBare>MENU)' + NB + W_S + '$' +
    ')',
  'i'
)

/** 结束一个块用哪个 END <TYPE>。 */
const TERMINATOR: Record<string, string> = {
  FUNCTION: 'FUNCTION', MAIN: 'MAIN', REPORT: 'REPORT', DIALOG: 'DIALOG',
  INPUT: 'INPUT', DISPLAY: 'DISPLAY', CONSTRUCT: 'CONSTRUCT', MENU: 'MENU',
}
/** 模块级单元:永不嵌套,且它们的 END 能修复夹在中间未闭合的 UI 块。 */
const MODULE_KINDS = new Set(['FUNCTION', 'MAIN', 'REPORT'])

/** 同行开闭(文档中合法):MENU "t" COMMAND "Quit" EXIT MENU END MENU */
const RE_SAME_LINE_END: Record<string, RegExp> = {}
for (const k of Object.keys(TERMINATOR)) {
  RE_SAME_LINE_END[k] = new RegExp('(?:^|[^A-Za-z0-9_])END' + W_P + TERMINATOR[k] + NB, 'i')
}

/** 未闭合的非模块帧在 EOF 处的跨度上限,防止被截断的文件吞掉整个文档。 */
const MAX_UNCLOSED_SPAN = 10000

/* ============================ 剥注释 / 剥字符串 ============================ */

/**
 * 生成两份结构相同(等长、逐字符对齐)的行数组:
 *
 * - `code`:注释**和字符串内容**都抹成空格 —— 用于结构匹配。
 *   抹字符串内容是必须的:语料里有跨行 SQL 字面量,其续行以关键字开头,
 *   例如多行 `"SELECT …"` 字面量续行上的
 *   `ON t1.col_a = t2.col_a AND ...`(JOIN 条件),不抹就会被当成 dialog 子块。
 * - `disp`:只抹注释、保留字符串 —— 用于取标签文字(例如 MENU 的标题)。
 *
 * 两份都在同一趟里产生,保证下标完全一致。引号状态跨行保持(跨行字面量),
 * `\r` 原样保留,这样下标与编辑器一致。
 */
function maskLines(text: string): { code: string[]; disp: string[] } {
  const code: string[] = []
  const disp: string[] = []
  let c = ''
  let d = ''
  let quote = 0
  let inBrace = false

  const both = (s: string): void => { c += s; d += s }

  for (let i = 0, n = text.length; i < n; i++) {
    const ch = text[i]
    const cc = text.charCodeAt(i)

    if (cc === CH_LF) { code.push(c); disp.push(d); c = ''; d = ''; continue }

    // 行尾的 `\r` 必须丢掉(匹配用的副本带 `\r` 会让 `.*$` 这类静默失效),
    // 而且**单独的 `\r` 也是行终止符** —— 与 Monaco 的文档行模型
    // (`/\r\n|\r|\n/`)一致。只按 `\n` 断行的话,含孤立 `\r` 的文件从那一行起
    // 所有行号都会偏 1,大纲整体上移一行(真实语料里存在这种字节)。
    if (cc === CH_CR) {
      if (text.charCodeAt(i + 1) === CH_LF) continue // CRLF:丢掉 \r,由下一轮的 \n 断行
      code.push(c); disp.push(d); c = ''; d = ''     // 孤立 \r:断行
      continue
    }

    // `{ … }` 是块注释,不可嵌套。内容两份都抹掉(注释里没有需要保留的字符串文字),
    // 于是花括号注释里的关键字不会变成幻影节点 —— 真实存在把整段 `ON ACTION …`
    // 包在 `{ … }` 里的代码。
    if (inBrace) {
      if (cc === CH_RB) inBrace = false
      both(' ')
      continue
    }

    if (quote) {
      // `\` 在 ' 和 " 里也是转义(只有反引号是原始字符串)。
      // 漏掉这一步的后果非常严重:`'\''`(转义引号紧跟收尾引号)里的第一个 '
      // 会被「成对引号是字面引号」规则连同一个收尾 ' 一起吃掉,引号状态从此
      // 永久卡在字符串里,其后整片代码被掩成空格 —— 只漏节点、不造幻影,
      // 所以极难察觉(实测一个 18,305 行的模块整份只剩 1 个假节点)。
      if (cc === CH_BS) {
        const nx = text.charCodeAt(i + 1)
        if (quote === CH_BT || nx === quote || nx === CH_BS) {
          c += '  '; d += text[i] + text[i + 1]; i++; continue
        }
      }
      if (quote === CH_BT && cc === CH_BT) { quote = 0; both(ch); continue }
      if (cc === quote) {
        if (quote !== CH_BT && text.charCodeAt(i + 1) === quote) {
          c += '  '; d += ch + ch; i++; continue
        }
        quote = 0; both(ch); continue
      }
      c += ' '; d += ch; continue // 字符串内容:code 抹成空格,disp 原样
    }

    if (cc === CH_SQ || cc === CH_DQ || cc === CH_BT) { quote = cc; both(ch); continue }
    if (cc === CH_LB) { inBrace = true; both(' '); continue }
    // `--` 与 `#` 同为行注释。不认 `--` 的后果很隐蔽:注释里的撇号会开启**跨行**
    // 字符串状态,其后整片代码被掩成空格 —— 只漏节点、不造幻影。实测一条形如
    // `--   ,field ='",var,"',` 的注释就让一个模块丢掉成片函数与整个 DIALOG 容器。
    if (cc === CH_HASH || (cc === CH_DASH && text.charCodeAt(i + 1) === CH_DASH)) {
      both(' ')
      while (i + 1 < n && text.charCodeAt(i + 1) !== CH_LF) { both(' '); i++ }
      continue
    }
    both(ch)
  }
  code.push(c)
  disp.push(d)
  return { code, disp }
}

/* ============================ 节点 ============================ */

interface RawNode {
  kind: OutlineKind
  label: string
  startLine: number
  endLine: number
  selStart: number
  selEnd: number
  children: RawNode[]
}

const normalize = (s: string): string => s.replace(/[ \t]+/g, ' ').trim().replace(/[.,]+$/, '')

function describe(mo: RegExpExecArray): { kind: OutlineKind; label: string; nameCol?: number } | null {
  const g = mo.groups as Record<string, string | undefined>
  if (g.fn) {
    return {
      kind: 'FUNCTION',
      label: g.fnName!,
      // 函数名在参数表的 '(' 之前,必须从整个匹配里定位,否则会得到负偏移。
      nameCol: mo.index + Math.max(0, mo[0].lastIndexOf(g.fnName!)),
    }
  }
  if (g.main) return { kind: 'MAIN', label: 'MAIN' }
  if (g.report) return { kind: 'REPORT', label: g.reportName ? 'REPORT ' + g.reportName : 'REPORT' }
  // 过程式 DIALOG 没有名字;模块级声明式 DIALOG name(params) 有,必须带上,
  // 否则同一模块里的多个声明式 DIALOG 会全部同名。
  if (g.dialog) return { kind: 'DIALOG', label: g.dialogName ? 'DIALOG ' + g.dialogName : 'DIALOG' }
  if (g.input || g.inputBare) {
    const arg = g.inputByName ?? g.inputArray ?? g.inputRecord ?? ''
    return { kind: 'INPUT', label: normalize('INPUT ' + arg) }
  }
  if (g.display) return { kind: 'DISPLAY', label: normalize('DISPLAY ARRAY ' + g.displayArray) }
  if (g.construct) {
    const by = g.constructBy ? 'BY NAME ' : ''
    return { kind: 'CONSTRUCT', label: normalize('CONSTRUCT ' + by + g.constructName) }
  }
  // menuAttr 是无标题 MENU 直接跟属性表,menuText 为 undefined → 标签就是 "MENU"
  if (g.menu || g.menuBare || g.menuAttr) {
    return { kind: 'MENU', label: normalize('MENU ' + (g.menuText ?? '')) }
  }
  return null
}

/**
 * 单趟前向扫描 + 显式栈。栈底是虚拟 ROOT,使每个节点都有父节点。
 *
 * 推入:块开头作为栈顶的子节点推入且**不弹** SUB 帧(这正是 MENU 能嵌进
 * ON ACTION 的原因);子块开头先弹掉所有 SUB 帧(它们是兄弟,永不互相嵌套)。
 * 弹出:END <TYPE> 自上而下跳过 SUB 帧,第一个非 SUB 帧的 kind 必须匹配,
 * 否则忽略该终止符(绝不猜)。
 */
function scanBlocks(code: string[], disp: string[]): RawNode[] {
  const root: RawNode = {
    kind: 'FUNCTION', label: '', startLine: 0, endLine: code.length - 1,
    selStart: 0, selEnd: 0, children: [],
  }
  const stack: { kind: OutlineKind | 'ROOT'; node: RawNode }[] = [{ kind: 'ROOT', node: root }]

  const closeTop = (endLine: number): void => {
    const f = stack.pop()!
    f.node.endLine = Math.max(endLine, f.node.startLine)
  }

  /**
   * 模块级 END 修复 / EOF 兜底时关闭栈顶。
   *
   * 块帧若**一条子节点都没有**,说明它是「无 control block 的裸语句」形式:
   * 文档把 `END <TYPE>` 和 control block 放在**同一个可选组**里 —— 没有 control block
   * 就没有 END,它只是一条单行语句。此时 range 不该延伸到父块末尾,
   * 否则 `INPUT g_cust` 会把后面 `DISPLAY "done"` 这类兄弟语句吞进范围。
   *
   * 有子节点的块(真有 control block)仍按原样收尾 —— 语料里大量块省略了
   * `END INPUT`,靠模块级 END 修复是对的。
   */
  const closeTopRepair = (endLine: number): void => {
    const f = stack.pop()!
    const isModule = MODULE_KINDS.has(f.kind as string)
    const bare = f.kind !== 'SUB' && !isModule && f.node.children.length === 0
    const end = bare
      ? f.node.startLine
      : isModule
        ? endLine
        : Math.min(endLine, f.node.startLine + MAX_UNCLOSED_SPAN)
    f.node.endLine = Math.max(end, f.node.startLine)
  }

  for (let i = 0, n = code.length; i < n; i++) {
    const line = code[i]
    if (RE_INPUT_OPTION.test(line)) continue

    /* --- 终止符 --- */
    const me = RE_END.exec(line)
    if (me) {
      const type = me[1].toUpperCase()
      let k = stack.length - 1
      if (MODULE_KINDS.has(type)) {
        // 函数体不能嵌套:向下扫时不限定只跳 SUB,以此修复中间未闭合的 UI 块。
        while (k > 0 && stack[k].kind !== type) k--
      } else {
        while (k > 0 && stack[k].kind === 'SUB') k--
      }
      if (k > 0 && stack[k].kind === type) {
        // 之间被夹住的帧用 repair 口径收尾(裸语句块收成单行)
        while (stack.length - 1 > k) closeTopRepair(i - 1)
        closeTop(i)
      }
      continue
    }

    /* --- 子块开头:隐式结束上一个子块 --- */
    const ms = RE_SUB.exec(line)
    if (ms) {
      while (stack.length > 1 && stack[stack.length - 1].kind === 'SUB') closeTop(i - 1)
      // 标签取 disp(保留字符串原文)—— 用 code 会让 `COMMAND "Quit" "Leave"`
      // 变成 `COMMAND " " " "`,因为字符串内容在 code 里已被抹成空格。
      const msd = RE_SUB.exec(disp[i]) ?? ms
      let label = normalize(msd.groups!.head.toUpperCase() + msd.groups!.rest)
      // COMMAND 的完整形式是 `COMMAND [KEY (…)] option-name [option-comment] [HELP n]`,
      // 只把 option-name 放进标签 —— 否则 `COMMAND "Apply" "Applies" HELP 100` 整串都成了名字。
      // 末尾的 `(?:\[[^\]]*\])*` 保留数组下标 —— 否则 `COMMAND itemV[1]` 会退化成
      // `COMMAND itemV`,同一 MENU 下多个项变成一堆同名标签。
      const cmd = /^(COMMAND(?:\s+KEY\s*\([^)]*\))?)\s+("[^"]*"|'[^']*'|`[^`]*`|[A-Za-z_]\w*(?:\[[^\]]*\])*)/i.exec(
        msd.groups!.head + msd.groups!.rest
      )
      if (cmd) label = normalize(cmd[1].toUpperCase() + ' ' + cmd[2])

      const node: RawNode = {
        kind: 'SUB',
        label,
        startLine: i, endLine: i,
        selStart: ms.index, selEnd: ms.index + ms[0].length,
        children: [],
      }
      stack[stack.length - 1].node.children.push(node)
      // SUB 也必须是栈帧:MENU 会真实地嵌在 ON ACTION 里。
      // 当成叶子的话,那个 MENU 会被错挂到 DISPLAY ARRAY 上。
      stack.push({ kind: 'SUB', node })
      continue
    }

    /* --- 块开头 --- */
    const mo = RE_OPEN.exec(line)
    if (!mo) continue
    // 检测用 code(字符串已抹),标签用 disp(保留字符串原文,例如 MENU 标题)。
    // 两份等长同构,匹配的下标与跨度完全一致,只差字符串内容。
    const d = describe(RE_OPEN.exec(disp[i]) ?? mo)
    if (!d) continue

    if (MODULE_KINDS.has(d.kind)) {
      while (stack.length > 1) closeTop(i - 1)
    }

    const node: RawNode = {
      kind: d.kind,
      label: d.label,
      startLine: i, endLine: i,
      selStart: d.nameCol ?? mo.index,
      selEnd: d.nameCol !== undefined ? d.nameCol + d.label.length : mo.index + mo[0].length,
      children: [],
    }
    stack[stack.length - 1].node.children.push(node)

    // 同行开闭,如 MENU "t" COMMAND "Quit" EXIT MENU END MENU
    if (RE_SAME_LINE_END[d.kind].test(line.slice(mo.index + mo[0].length))) node.endLine = i
    else stack.push({ kind: d.kind, node })
  }

  // EOF:仍未闭合的帧结束于此(裸语句块收成单行)
  while (stack.length > 1) closeTopRepair(code.length - 1)

  fixup(root)
  return root.children
}

/**
 * 只可能把父节点的 endLine 往大改,永不改子节点 —— 于是
 * child.range ⊆ parent.range 由构造保证。
 *
 * 用显式栈而非递归:嵌套足够深时递归会抛 Maximum call stack size exceeded
 * (正常语料最大深度只有 7,但粘贴一份畸形文件就能触发)。
 */
function fixup(root: RawNode): void {
  const order: RawNode[] = []
  const stack: RawNode[] = [root]
  while (stack.length > 0) {
    const n = stack.pop()!
    if (n.endLine < n.startLine) n.endLine = n.startLine
    order.push(n)
    for (const c of n.children) stack.push(c)
  }
  // 前序的逆序 = 子节点必先于其父节点处理
  for (let i = order.length - 1; i >= 0; i--) {
    const n = order[i]
    for (const c of n.children) if (c.endLine > n.endLine) n.endLine = c.endLine
  }
}

/** 转对外节点:行号 0-based → 1-based,并沿用 BDL 的钳位(非法范围会被宿主拒绝)。 */
function makeNode(nd: RawNode, raw: string[]): OutlineNode {
  const endLine0 = Math.min(nd.endLine, raw.length - 1)
  const lineLen = raw[nd.startLine]?.length ?? 0
  const selEnd = Math.min(nd.selEnd, lineLen)
  const selStart = Math.min(nd.selStart, selEnd)
  return {
    label: nd.label,
    kind: nd.kind,
    line: nd.startLine + 1,
    endLine: endLine0 + 1,
    selStart,
    selEnd,
  }
}

/** 同样用显式栈,理由见 fixup:深嵌套不得让遍历栈溢出。 */
function toOutlineNodes(roots: RawNode[], raw: string[]): OutlineNode[] {
  const map = new Map<RawNode, OutlineNode>()
  const order: RawNode[] = []
  const stack = [...roots].reverse()
  while (stack.length > 0) {
    const n = stack.pop()!
    order.push(n)
    map.set(n, makeNode(n, raw))
    for (let i = n.children.length - 1; i >= 0; i--) stack.push(n.children[i])
  }
  for (const n of order) {
    const kids = n.children.map((c) => map.get(c)!)
    if (kids.length > 0) map.get(n)!.children = kids
  }
  return roots.map((n) => map.get(n)!)
}

/**
 * 解析 4GL 源码为大纲树(供大纲面板点击跳行)。
 * 传入的应是**未做行号补偿**的原文:调试页顶部前插的空行不计入 line,
 * 跳转时由调用方自行 + lineOffset。
 */
export function parseOutline(text: string): OutlineNode[] {
  // 与 Monaco 的文档行模型一致:`/\r\n|\r|\n/`。用 split('\n') 会让 CRLF 文件的行尾
  // 留着 `\r`,含孤立 `\r` 的文件还会整体偏 1 行。
  const raw = text.split(/\r\n|\r|\n/)
  const { code, disp } = maskLines(text)
  return toOutlineNodes(scanBlocks(code, disp), raw)
}
