/**
 * 大纲解析器对拍:用 BDL 的「文档推导夹具」验证 web/src/fgloutline.ts。
 *
 *   cd web && npm run check:outline
 *   node scripts/.tmp-outline.mjs [--strict] [--filter 子串] [--list] [--tree]
 *
 * 判据与 BDL 侧一致:期望值写在 fgl-fixtures/<name>.expected.json 里,由 BDL 官方文档推导,
 * **不是**从实现跑出来的快照 —— 不符时先查实现;确实要改期望,必须同时补 deviation 说明。
 * 带 deviation 的用例是已知偏差,默认不计失败(用 --strict 可把它们也算作失败)。
 *
 * 夹具来自 D:\我的项目\BDL\test\fixtures(同作者,MIT),复制说明见 fgl-fixtures/README.md。
 * 基线(与 BDL 自身 fixture-check 实测一致):用例 61 个:通过 58,已知偏差 3,不符 0。
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { parseOutline } from '../src/fgloutline'

const DIR = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fgl-fixtures')

/** OutlineKind → BDL 期望文件里的 kind 名(与 BDL 的 vscode.SymbolKind 映射一致) */
const KIND_NAME = {
  FUNCTION: 'Function',
  MAIN: 'Module',
  REPORT: 'Module',
  DIALOG: 'Interface',
  INPUT: 'Object',
  CONSTRUCT: 'Object',
  DISPLAY: 'Array',
  MENU: 'Namespace',
  SUB: 'Event',
}

const argv = process.argv.slice(2)
const opt = { strict: false, filter: null, list: false, tree: false, maxDiffs: 12 }
for (let i = 0; i < argv.length; i++) {
  const a = argv[i]
  if (a === '--strict') opt.strict = true
  else if (a === '--filter') opt.filter = argv[++i]
  else if (a === '--list') opt.list = true
  else if (a === '--tree') opt.tree = true
  else if (a === '-h' || a === '--help') {
    console.log('用法: npm run check:outline -- [--strict] [--filter 子串] [--list] [--tree]')
    process.exit(0)
  }
}

if (!fs.existsSync(DIR)) {
  console.error(`缺少用例目录: ${DIR}`)
  process.exit(2)
}

/* ------------------------------ 载入期望 ------------------------------ */
const cases = []
for (const f of fs.readdirSync(DIR).sort()) {
  if (!f.endsWith('.expected.json')) continue
  const name = f.slice(0, -'.expected.json'.length)
  const srcPath = path.join(DIR, name + '.4gl')
  if (!fs.existsSync(srcPath)) {
    cases.push({ name, error: `缺少源文件 ${name}.4gl` })
    continue
  }
  let exp
  try {
    exp = JSON.parse(fs.readFileSync(path.join(DIR, f), 'utf8'))
  } catch (e) {
    cases.push({ name, error: `期望 JSON 解析失败: ${e.message}` })
    continue
  }
  cases.push({ name, srcPath, exp })
}

const selected = cases.filter((c) => !opt.filter || c.name.includes(opt.filter))
if (selected.length === 0) {
  console.error(`没有匹配的用例${opt.filter ? `（--filter ${opt.filter}）` : ''}`)
  process.exit(2)
}

if (opt.list) {
  for (const c of selected) {
    console.log(`${c.exp && c.exp.deviation ? '⚠' : ' '} ${c.name}`)
    if (c.exp) {
      console.log(`    doc:   ${c.exp.doc}`)
      console.log(`    about: ${c.exp.about ?? ''}`)
      if (c.exp.deviation) console.log(`    dev:   ${c.exp.deviation}`)
    }
  }
  process.exit(0)
}

/* ------------------------------ 跑实现 ------------------------------ */

// OutlineNode(1-based 行) → 期望文件口径(0-based 行)
const toPlain = (n) => ({
  name: n.label,
  kind: KIND_NAME[n.kind] ?? String(n.kind),
  startLine: n.line - 1,
  endLine: n.endLine - 1,
  selection: [n.selStart, n.selEnd],
  children: (n.children ?? []).map(toPlain),
})

const renderTree = (nodes, depth = 0) => {
  const out = []
  for (const n of nodes) {
    out.push(`${'  '.repeat(depth)}${n.name}  [${n.kind} L${n.startLine + 1}-${n.endLine + 1}]`)
    out.push(...renderTree(n.children ?? [], depth + 1))
  }
  return out
}

/* ------------------------------ 对拍 ------------------------------ */
const diffs = []
function compare(exp, act, at) {
  const n = Math.max(exp.length, act.length)
  for (let i = 0; i < n; i++) {
    const e = exp[i]
    const a = act[i]
    const where = `${at}[${i}]`
    if (!e) { diffs.push(`${where} 多出节点 ${a.name} [${a.kind} L${a.startLine + 1}]`); continue }
    if (!a) { diffs.push(`${where} 缺少节点 ${e.name} [${e.kind} L${e.startLine + 1}]`); continue }
    const label = `${where} ${e.name}@L${e.startLine + 1}`
    if (e.name !== a.name) diffs.push(`${label}: name 期望 ${JSON.stringify(e.name)}，实际 ${JSON.stringify(a.name)}`)
    if (e.kind !== a.kind) diffs.push(`${label}: kind 期望 ${e.kind}，实际 ${a.kind}`)
    if (e.startLine !== a.startLine) diffs.push(`${label}: startLine 期望 ${e.startLine}，实际 ${a.startLine}`)
    if (e.endLine !== a.endLine) diffs.push(`${label}: endLine 期望 ${e.endLine}，实际 ${a.endLine}`)
    if (e.selection && (e.selection[0] !== a.selection[0] || e.selection[1] !== a.selection[1])) {
      diffs.push(`${label}: selectionRange 期望 [${e.selection.join(',')}]，实际 [${a.selection.join(',')}]`)
    }
    compare(e.children ?? [], a.children ?? [], `${at}[${i}].children`)
  }
}

let fatal = 0, known = 0, pass = 0, crashed = 0
const report = []

for (const c of selected) {
  if (c.error) {
    fatal++
    report.push(`\n❌ ${c.name}\n    - ${c.error}`)
    continue
  }
  const text = fs.readFileSync(c.srcPath, 'utf8')
  let nodes
  try {
    nodes = parseOutline(text)
  } catch (e) {
    crashed++
    fatal++
    report.push(`\n❌ ${c.name}\n    - 解析器抛异常: ${e.message}`)
    continue
  }
  const actual = (nodes ?? []).map(toPlain)

  diffs.length = 0
  compare(c.exp.nodes ?? [], actual, 'root')
  const d = diffs.slice(0, opt.maxDiffs)
  const more = diffs.length - d.length

  if (d.length === 0) {
    pass++
    if (opt.tree) {
      report.push(`\n✅ ${c.name}\n${renderTree(actual).map((l) => '    ' + l).join('\n')}`)
    }
    continue
  }

  const isKnown = !!c.exp.deviation && !opt.strict
  if (isKnown) known++; else fatal++

  report.push(
    `\n${isKnown ? '⚠️ ' : '❌'} ${c.name}  (${c.exp.doc})\n` +
    `    考什么: ${c.exp.about ?? ''}\n` +
    (isKnown ? `    已知偏差: ${c.exp.deviation}\n` : '') +
    d.map((x) => '    - ' + x).join('\n') +
    (more > 0 ? `\n    … 另有 ${more} 处差异` : '') +
    `\n    实际树:\n${renderTree(actual).map((l) => '      ' + l).join('\n')}`
  )
}

/* ------------------------------ 报告 ------------------------------ */
console.log(`用例 ${selected.length} 个：通过 ${pass}，已知偏差 ${known}，不符 ${fatal}${crashed ? `，解析器抛异常 ${crashed}` : ''}`)
console.log(report.join('\n'))

if (fatal) {
  console.error(
    `\n失败: ${fatal} 个用例与文档推导的期望树不符。\n` +
    `判据是文档，不是实现 —— 若确认是实现错，改 src/fgloutline.ts；若确认期望写错，改 fgl-fixtures/*.expected.json。\n` +
    `（${known} 个已知偏差已由期望文件里的 "deviation" 字段声明，用 --strict 可把它们也变成失败。）`
  )
  process.exit(1)
}
console.log(`\n✅ 全部通过${known ? `（另有 ${known} 个已在期望文件里声明的已知偏差）` : ''}。`)
