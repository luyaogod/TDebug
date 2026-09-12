// 4GL(BDL)源码大纲解析:提取 FUNCTION/MAIN 与交互语句(DIALOG/CONSTRUCT/INPUT/INPUT ARRAY)
// 及其子块(BEFORE/AFTER/ON ACTION/...)构成树,供大纲面板点击跳行。
// 依据 BDL 文档(D:\T100\4gl文档\BDL-Markdown):自由格式、块由关键字对界定与缩进无关;
// 注释有 --、#、{...}(可跨行);DIALOG 恒有 END DIALOG,而 INPUT/CONSTRUCT/DISPLAY ARRAY/
// PROMPT 的控制块与 END X 可选(单语句形式,嵌套时需以 ; 收尾),INPUT ARRAY 的 END 也是 END INPUT。
// 收录范围(最小):函数 = FUNCTION+MAIN;交互块 = DIALOG/CONSTRUCT/INPUT/INPUT ARRAY;
// REPORT/MENU/PROMPT/独立 DISPLAY ARRAY 不上树,但仍跟踪其 END 参与嵌套,防止错位。

export interface OutlineNode {
  label: string
  line: number // 1-based,磁盘源码行(调试页显示/跳转时需 + lineOffset)
  children?: OutlineNode[]
}

type Kind =
  | 'function' | 'main' | 'report'
  | 'dialog' | 'construct' | 'input' | 'inputarray' | 'displayarray'
  | 'menu' | 'prompt'

// 只有标记为输出的种类才上树
const EMITTED: Record<Kind, boolean> = {
  function: true, main: true, dialog: true, construct: true, input: true, inputarray: true,
  report: false, displayarray: false, menu: false, prompt: false,
}

interface Frame {
  kind: Kind
  node?: OutlineNode // 未输出的种类无节点
  tentative: boolean // input/construct/displayarray/prompt 入栈时尚不确定有无控制块
}

// 剥离一行内的注释与字符串字面量;inBlock 为 {...} 块注释的跨行状态
function stripCode(line: string, inBlock: boolean): [string, boolean] {
  let out = ''
  let i = 0
  const n = line.length
  while (i < n) {
    if (inBlock) {
      const j = line.indexOf('}', i)
      if (j < 0) return [out, true]
      i = j + 1
      inBlock = false
      continue
    }
    const c = line[i]
    if (c === '-' && line[i + 1] === '-') break
    if (c === '#') break
    if (c === '{') { inBlock = true; i++; continue }
    if (c === '"' || c === "'") {
      const q = c
      i++
      while (i < n && line[i] !== q) i++
      i++
      out += ' ' // 字面量占一个空格,避免相邻 token 拼接出关键字
      continue
    }
    out += c
    i++
  }
  return [out, inBlock]
}

// 子块关键字 → 所属语句种类;就近向上找第一个种类匹配且已输出的祖先挂载
const SUBS: [RegExp, Kind[]][] = [
  [/^BEFORE\s+DIALOG\b|^AFTER\s+DIALOG\b/, ['dialog']],
  [/^BEFORE\s+CONSTRUCT\b|^AFTER\s+CONSTRUCT\b/, ['construct']],
  [/^BEFORE\s+INPUT\b|^AFTER\s+INPUT\b/, ['input', 'inputarray']],
  [/^BEFORE\s+ROW\b|^AFTER\s+ROW\b|^ON\s+ROW\s+CHANGE\b|^BEFORE\s+INSERT\b|^AFTER\s+INSERT\b|^BEFORE\s+DELETE\b|^AFTER\s+DELETE\b|^ON\s+SORT\b/, ['inputarray']],
  [/^BEFORE\s+FIELD\b|^AFTER\s+FIELD\b|^ON\s+CHANGE\b/, ['input', 'inputarray', 'construct', 'displayarray']],
  [/^ON\s+ACTION\b|^ON\s+KEY\b|^ON\s+IDLE\b|^ON\s+TIMER\b/, ['dialog', 'construct', 'input', 'inputarray', 'displayarray', 'menu', 'prompt']],
  [/^COMMAND\b/, ['dialog', 'menu']],
  [/^SUBDIALOG\b/, ['dialog']],
]

const END_KINDS = ['FUNCTION', 'MAIN', 'REPORT', 'DIALOG', 'INPUT', 'CONSTRUCT', 'DISPLAY', 'MENU', 'PROMPT'] as const
const END_MAP: Record<(typeof END_KINDS)[number], Kind[]> = {
  FUNCTION: ['function'], MAIN: ['main'], REPORT: ['report'], DIALOG: ['dialog'],
  INPUT: ['input', 'inputarray'], CONSTRUCT: ['construct'], DISPLAY: ['displayarray'],
  MENU: ['menu'], PROMPT: ['prompt'],
}

const trunc = (s: string) => (s.length > 60 ? s.slice(0, 57) + '...' : s)

export function parseOutline(text: string): OutlineNode[] {
  const roots: OutlineNode[] = []
  const stack: Frame[] = []
  let inBlock = false
  let lineNo = 0

  const attach = (node: OutlineNode) => {
    for (let i = stack.length - 1; i >= 0; i--) {
      const f = stack[i]
      if (f.node) {
        ;(f.node.children ??= []).push(node)
        return
      }
    }
    roots.push(node)
  }
  const push = (kind: Kind, label: string, line: number, tentative: boolean) => {
    const node = EMITTED[kind] ? { label, line } : undefined
    if (node) attach(node)
    stack.push({ kind, node, tentative })
  }
  // 语句开始前收掉顶部连续的 tentative 块(它们是单语句形式,没有控制块)
  const closeTentative = () => {
    while (stack.length && stack[stack.length - 1].tentative) stack.pop()
  }
  // 单语句以 ; 收尾(BDL 嵌套规则):入栈后立即弹出
  const popIfTentative = (t: string) => {
    if (t.endsWith(';') && stack.length && stack[stack.length - 1].tentative) stack.pop()
  }

  for (const raw of text.split('\n')) {
    lineNo++
    const [stripped, still] = stripCode(raw, inBlock)
    inBlock = still
    const t = stripped.trim()
    if (!t) continue
    const U = t.toUpperCase()

    // 1) END 块结束:就近找种类匹配的栈帧弹出(其上方残留按叶子留在树上)
    const em = /^END\s+(FUNCTION|MAIN|REPORT|DIALOG|INPUT|CONSTRUCT|DISPLAY|MENU|PROMPT)\b/.exec(U)
    if (em) {
      const kinds = END_MAP[em[1] as (typeof END_KINDS)[number]]
      for (let i = stack.length - 1; i >= 0; i--) {
        if (kinds.includes(stack[i].kind)) {
          stack.length = i
          break
        }
      }
      continue
    }

    // 2) 顶层块(FUNCTION/MAIN/REPORT/声明式 DIALOG):直接重置栈(容错缺 END 的异常源码)
    let m = /^(?:(?:PUBLIC|PRIVATE)\s+)?FUNCTION\s+(\w+)/.exec(U)
    if (m) {
      stack.length = 0
      push('function', `${m[1].toLowerCase()}()`, lineNo, false)
      continue
    }
    if (/^MAIN\b/.test(U)) {
      stack.length = 0
      push('main', 'MAIN', lineNo, false)
      continue
    }
    m = /^(?:(?:PUBLIC|PRIVATE)\s+)?REPORT\s+(\w+)/.exec(U)
    if (m) {
      stack.length = 0
      push('report', m[1], lineNo, false)
      continue
    }
    // 声明式 DIALOG(模块级,带名参);排除过程式的 DIALOG ATTRIBUTES(...)
    m = /^(?:(?:PUBLIC|PRIVATE)\s+)?DIALOG\s+(?!ATTRIBUTES\b)(\w+)\s*\(/.exec(U)
    if (m) {
      stack.length = 0
      push('dialog', `DIALOG ${m[1].toLowerCase()}()`, lineNo, false)
      continue
    }

    // 3) 过程式交互语句(先长后短:INPUT ARRAY 先于 INPUT)
    if (/^INPUT\s+ARRAY\b/.test(U)) {
      closeTentative()
      push('inputarray', 'INPUT ARRAY', lineNo, true)
      popIfTentative(t)
      continue
    }
    if (/^INPUT\b/.test(U)) {
      closeTentative()
      push('input', 'INPUT', lineNo, true)
      popIfTentative(t)
      continue
    }
    if (/^CONSTRUCT\b/.test(U)) {
      closeTentative()
      push('construct', 'CONSTRUCT', lineNo, true)
      popIfTentative(t)
      continue
    }
    if (/^DIALOG\b/.test(U)) {
      closeTentative()
      push('dialog', 'DIALOG', lineNo, false)
      continue
    }
    if (/^DISPLAY\s+ARRAY\b/.test(U)) {
      closeTentative()
      push('displayarray', 'DISPLAY ARRAY', lineNo, true)
      popIfTentative(t)
      continue
    }
    if (/^MENU\b/.test(U)) {
      closeTentative()
      push('menu', 'MENU', lineNo, false)
      continue
    }
    if (/^PROMPT\b/.test(U)) {
      closeTentative()
      push('prompt', 'PROMPT', lineNo, true)
      popIfTentative(t)
      continue
    }

    // 4) 子块行:就近向上找种类匹配且已输出的祖先;弹掉其上方游离的 tentative 块
    for (const [re, kinds] of SUBS) {
      if (!re.test(U)) continue
      for (let i = stack.length - 1; i >= 0; i--) {
        const f = stack[i]
        if (f.node && kinds.includes(f.kind)) {
          stack.length = i + 1
          f.tentative = false
          ;(f.node.children ??= []).push({ label: trunc(t), line: lineNo })
          break
        }
      }
      break
    }
    // 其余行(普通语句/SQL 续行/IF 等)不影响大纲
  }
  return roots
}
