/**
 * 4GL 高亮对拍:用**真实的 Monaco Monarch 引擎**验证 web/src/fglTokens.ts。
 *
 *   cd web && npm run check:tokens
 *
 * 为什么不用手写正则模拟器:引擎的规则优先级、`^` 锚定与捕获组语义都和朴素模拟不同,
 * 模拟器一宽松就会给假绿灯。这里用真 Monaco 的 monarchCompile.compile() + MonarchTokenizer,
 * 只 stub 三个与语法无关的外围服务(configurationService 的最大行长、主题、语言注册)。
 *
 * 三项断言(对应 BDL 侧 tokenize-check.mjs 的意图,但换成 Monarch 引擎):
 *   ① 对抗用例组:关键字边界、ON ACTION 动作名、点号成员、预处理器、转义引号、三类注释。
 *   ② 全部 61 个夹具源码逐行跑:任意 token 只能是 comment / string / keyword / 无(默认色),
 *      不得出现第四类。
 *   ③ 状态平衡:逐行喂完整个文件后,用最终 state 再 tokenize 一行哨兵,不得带 string/comment
 *      (即文件末尾没有未闭合的字符串/块注释)。
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { compile } from 'monaco-editor/esm/vs/editor/standalone/common/monarch/monarchCompile.js'
import { MonarchTokenizer } from 'monaco-editor/esm/vs/editor/standalone/common/monarch/monarchLexer.js'
import { FGL_MONARCH } from '../src/fglTokens'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const DIR = path.join(HERE, 'fgl-fixtures')

/** MonarchTokenizer 需要的外围服务(最小鸭子类型;与语法语义无关) */
function makeTokenizer() {
  const languageService = {
    languageIdCodec: { encodeLanguageId: (v) => v, decodeLanguageId: (v) => v },
    getLanguageId: () => '4gl',
    getLanguages: () => [],
    onDidChange: () => ({ dispose() {} }),
  }
  const themeService = {
    getColorTheme: () => ({ tokenColors: [] }),
    onColorThemeChange: () => ({ dispose() {} }),
  }
  const configService = {
    getValue: () => 20000, // editor.maxTokenizationLineLength
    onDidChangeConfiguration: () => ({ dispose() {} }),
  }
  return new MonarchTokenizer(languageService, themeService, '4gl', compile('4gl', FGL_MONARCH), configService)
}

const ALLOWED = new Set(['', 'comment', 'string', 'keyword'])
const kindOf = (t) => String(t.type).replace(/\.4gl$/, '')

/** token 只带 offset,长度 = 到下一个 token 的距离(最后一个到行尾) */
function spans(tokens, line) {
  return tokens.map((t, i) => ({
    kind: kindOf(t),
    start: t.offset,
    end: i + 1 < tokens.length ? tokens[i + 1].offset : line.length,
  }))
}

const kwTexts = (sp, line) => sp.filter((s) => s.kind === 'keyword').map((s) => line.slice(s.start, s.end))

/** 断言 [start,end) 整段都是 kind,且该 kind 不出现在别处 */
function coverCheck(sp, kind, start, end) {
  const inside = sp.filter((s) => s.start >= start && s.end <= end && s.kind === kind)
  const covered = inside.reduce((n, s) => n + (s.end - s.start), 0)
  if (covered !== end - start) return `[${start},${end}) 未被 ${kind} 完整覆盖(实际 ${covered} 字符)`
  const outside = sp.filter((s) => s.kind === kind && (s.start < start || s.end > end))
  if (outside.length) return `区间外还有 ${kind}: ${JSON.stringify(outside)}`
  return null
}

/* ------------------------------ ① 对抗用例 ------------------------------ */
// kw: 期望的关键字文本(按出现顺序)
// str / cmt: 期望被 string / comment 完整覆盖的 [start,end) 区间
const BATTERY = [
  { line: 'g_append = 1', kw: [], note: '词尾镶嵌的关键字不染色(Monarch 无 lookbehind,靠中性词符规则)' },
  { line: 'xEND', kw: [], note: '同上:前缀粘连' },
  { line: '2END', kw: [], note: '同上:数字前缀粘连' },
  { line: '_END', kw: [], note: '同上:下划线粘连' },
  { line: 'END2', kw: [], note: 'lookahead 有效:后缀粘连不染色' },
  { line: 'END_', kw: [], note: '同上' },
  { line: 'END定义', kw: ['END'], note: '紧邻中文仍染色(ASCII 前后瞻而非 \\b 的用意)' },
  { line: 'DEFINE r RECORD', kw: ['DEFINE', 'RECORD'], note: '普通关键字' },
  { line: 'DISPLAY ARRAY sa TO s.*', kw: ['DISPLAY', 'ARRAY', 'TO'], note: '数组形态' },
  { line: 'END REPORT', kw: ['END', 'REPORT'], note: '语句级关键字' },
  { line: 'g_browser.clear()', kw: [], note: '点号后成员/方法名不染色' },
  { line: 'g_qryparam.where = 1', kw: [], note: '同上(where 是关键字,但前面有点号)' },
  { line: 'ON ACTION next', kw: ['ON', 'ACTION'], note: 'ON ACTION 动作名不染色(即便动作名本身是关键字)' },
  { line: 'on action Delete', kw: ['on', 'action'], note: '同上;大小写不敏感' },
  { line: 'ON ACTION controlp INFIELD pmdldocno', kw: ['ON', 'ACTION', 'INFIELD'], note: '尾部 INFIELD 照常染色' },
  { line: 'ON t1.col_a = t2.col_a', kw: ['ON'], note: 'SQL JOIN 条件不是 dialog 子块' },
  { line: '&ifdef DEBUG', kw: [], note: '预处理器指令不染色' },
  { line: '&include "x.4gl"', kw: [], note: '同上' },
  { line: "LET s = 'it''s ok'", kw: ['LET'], note: '成对引号是字面引号:整对是一个字符串', str: [8, 18] },
  { line: 'cl_replace_str(x, \'\\\'\', \'\')', kw: [], note: "转义引号 '\\'' 不能让引号状态跑飞" },
  { line: '%"localized"', kw: [], note: '%" 本地化字符串', str: [0, 12] },
  { line: '`raw \\n text`', kw: [], note: '反引号原始字符串(不处理转义)', str: [0, 13] },
  { line: "-- it's a comment", kw: [], note: '-- 注释里的撇号不开字符串', cmt: [0, 17] },
  { line: '{ ON ACTION z }', kw: [], note: '花括号块注释内关键字不染色', cmt: [0, 15] },
  { line: '--#{ ON ACTION z --#}', kw: [], note: '--#{ --#} 块注释', cmt: [0, 21] },
  { line: 'LET a = 1 -- 行尾注释', kw: ['LET'], note: '行尾 -- 注释(语法文件不锚行首)', cmt: [10, 17] },
  { line: 'LET b = 2 # 井号注释', kw: ['LET'], note: '# 行注释', cmt: [10, 16] },
]

/* ------------------------------ 跑起来 ------------------------------ */
const t = makeTokenizer()
const state0 = await t.getInitialState()

let fail = 0
const bad = (msg) => { console.error('  ❌ ' + msg); fail++ }

console.log('① 对抗用例')
for (const c of BATTERY) {
  const r = await t.tokenize(c.line, false, state0)
  const sp = spans(r.tokens, c.line)
  const kinds = [...new Set(sp.map((s) => s.kind))]
  const stray = kinds.filter((k) => !ALLOWED.has(k))
  let problem = null
  if (stray.length) problem = `出现三类之外的 token: ${stray.join(',')}`
  else {
    const got = kwTexts(sp, c.line)
    if (got.join('|') !== c.kw.join('|')) problem = `关键字期望 [${c.kw.join(',')}]，实际 [${got.join(',')}]`
    else if (c.str) problem = coverCheck(sp, 'string', c.str[0], c.str[1])
    else if (c.cmt) problem = coverCheck(sp, 'comment', c.cmt[0], c.cmt[1])
  }
  if (problem) bad(`${JSON.stringify(c.line)} — ${problem}  (${c.note})`)
}
if (!fail) console.log(`  ✅ ${BATTERY.length} 个用例全过(关键字边界 / 中和规则 / 三类注释 / 转义)`)

/* ------------------------------ ② + ③ 夹具语料 ------------------------------ */
const files = fs.readdirSync(DIR).filter((f) => f.endsWith('.4gl')).sort()
let strayFiles = 0
const unbalanced = []
for (const f of files) {
  const text = fs.readFileSync(path.join(DIR, f), 'utf8')
  // 与 Monaco 的文档行模型一致(含孤立 \r 也断行)
  const lines = text.split(/\r\n|\r|\n/)
  let st = state0
  for (let i = 0; i < lines.length; i++) {
    const r = await t.tokenize(lines[i], false, st)
    st = r.endState
    for (const s of spans(r.tokens, lines[i])) {
      if (!ALLOWED.has(s.kind)) {
        if (strayFiles++ < 5) bad(`${f}:${i + 1} 出现第四类 token ${JSON.stringify(s.kind)}`)
      }
    }
  }
  // ③ 哨兵:文件末尾若还有未闭合的字符串/块注释,哨兵会被染色
  const sent = await t.tokenize('XXSENTINELXX', false, st)
  const sentKinds = spans(sent.tokens, 'XXSENTINELXX').map((s) => s.kind).filter((k) => k !== '')
  if (sentKinds.length) unbalanced.push(`${f}(${sentKinds.join(',')})`)
}

console.log(`\n② 夹具语料 ${files.length} 个文件逐行跑`)
if (strayFiles) console.log(`  ❌ ${strayFiles} 处出现第四类 token`)
else console.log('  ✅ 只出现 comment / string / keyword / 默认色 四类(前三 + 无色)')

console.log('\n③ 状态平衡(文件末尾哨兵不得被染色)')
// 基线(与 BDL 侧同样口径):夹具是**片段**,允许它们以未闭合状态结束。
// 这里的基线是首次实测结果 —— 若某文件的哨兵变脏或变干净,说明词法状态机被改动过。
const UNBALANCED_BASELINE = []
const cur = unbalanced.join(' ')
const base = UNBALANCED_BASELINE.join(' ')
if (cur !== base) {
  bad(`未闭合文件集合与基线不符:\n      期望 [${base}]\n      实际 [${cur}]`)
} else {
  console.log(`  ✅ 与基线一致(${UNBALANCED_BASELINE.length} 个未闭合片段)`)
}

console.log(fail === 0 ? '\n✅ 全部通过' : `\n${fail} 处失败`)
process.exit(fail === 0 ? 0 : 1)
