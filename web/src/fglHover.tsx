// 调试编辑器变量悬浮取值(仅 debug model 生效):
// - Monaco IContentWidget 承载悬浮卡片,锚定在变量词下方,随内容滚动
// - 卡片只显示值本身(无变量名/分割线):Record/ARRAY 复用侧边栏同款 VarTreeNodes
//   树并默认展开,标量显示原始文本;右上角悬停浮现复制按钮(复制 fgldb print 原始值)
// - 固定统一宽度(内容换行),内容超高时卡片内部滚动:滚轮在卡片上被拦截不上传,
//   避免长值滚动时编辑器代码跟着滚
// - 仅停站(stopped)时取值:走 api.print(fgldb print),按表达式缓存;
//   停站行变化(步进/落站)即清缓存保证取值新鲜
// - 先求值后弹卡:驻留 500ms 后静默调用 print,拿到结果才显示卡片——
//   No symbol(非变量)永不弹卡,其余错误直接以错误态弹出
import { useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { Copy } from 'lucide-react'
import * as monaco from 'monaco-editor'
import { useStore } from './store'
import { api } from './api'
import { parseFglTree } from './fglparse'
import { VarTreeNodes } from './VarTreeUi'

// 悬浮卡片:直接展示值(树/原始文本/错误)。只在拿到求值结果后弹出(无加载态,防闪烁)
function HoverCard({ v, e: err }: { v?: string; e?: string }) {
  const [copied, setCopied] = useState(false)
  const kids = v !== undefined ? parseFglTree(v) : null
  const copy = () => {
    if (v === undefined) return
    navigator.clipboard.writeText(v).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1200)
    })
  }
  return (
    <div className="fgl-hover">
      <button className="fgl-hover-copy" title="复制原始值" disabled={v === undefined} onClick={copy}>
        {copied ? '已复制' : <Copy className="h-3 w-3" />}
      </button>
      <div className="fgl-hover-body">
        {err ? (
          <div className="fgl-hover-value text-red-400">{err}</div>
        ) : kids ? (
          <VarTreeNodes nodes={kids} />
        ) : (
          <div className="fgl-hover-value">{v}</div>
        )}
      </div>
    </div>
  )
}

// 扩取光标处表达式:支持 record.field / a.b.c 链与 cust.* 结尾
function exprAt(model: monaco.editor.ITextModel, pos: monaco.Position): string | null {
  const w = model.getWordAtPosition(pos)
  if (!w) return null
  const line = model.getLineContent(pos.lineNumber)
  const isW = (ch: string) => /[\w]/.test(ch)
  let s = w.startColumn - 1
  let e = w.endColumn - 1
  while (e < line.length && line[e] === '.') {
    let j = e + 1
    if (line[j] === '*') { e = j + 1; break }
    if (!isW(line[j] ?? '')) break
    while (j < line.length && isW(line[j])) j++
    e = j
  }
  while (s > 0 && line[s - 1] === '.') {
    let i = s - 2
    while (i >= 0 && isW(line[i])) i--
    if (i + 1 >= s - 1) break
    s = i + 1
  }
  const expr = line.slice(s, e)
  return expr || null
}

// SQL / 4GL 常用关键字:取值后复制的"粗暴"变量识别时跳过,减少无效 print 往返
// (不在表里的标识符一律照试,由 fgldb 的 No symbol 判定)
const KEYWORDS = new Set(('select from where and or not in as on join left right inner outer full cross '
  + 'group by order having insert into values update set delete create table index view drop alter '
  + 'union all distinct case when then else end like between is null exists asc desc top limit offset '
  + 'define let display if then return for to while do call initialize continue exit when otherwise '
  + 'true false null record array of char char1 date integer decimal string varchar datetime '
  + 'main function procedure report schema require to end goto label sleep run exit program').split(/\s+/))

// 从文本提取候选变量 token(标识符链 a.b.c 与 cust.* 两种形态),去重、滤关键字
function candidateVars(text: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  const re = /(?:[A-Za-z_][A-Za-z0-9_]*\.)+\*|[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*/g
  for (const m of text.matchAll(re)) {
    const t = m[0]
    if (KEYWORDS.has(t.toLowerCase()) || seen.has(t)) continue
    seen.add(t)
    out.push(t)
  }
  return out
}

// 取值结果转 SQL 可嵌入字面量:双引号字符串 → 单引号(内部单引号 '' 转义);其余原样
function valueForSql(v: string): string {
  const t = v.trim()
  if (t.length >= 2 && t.startsWith('"') && t.endsWith('"')) {
    return "'" + t.slice(1, -1).replace(/'/g, "''") + "'"
  }
  return t
}

// 剪贴板写入(测试可 patch 捕获)
function writeClipboard(text: string): Promise<void> {
  return navigator.clipboard.writeText(text)
}

let attachedTo: monaco.editor.IStandaloneCodeEditor | null = null

// 在 SourceView onMount 里调用(单编辑器实例)
export function attachHover(editor: monaco.editor.IStandaloneCodeEditor) {
  if (attachedTo === editor) return
  attachedTo = editor

  const dom = document.createElement('div')
  dom.style.display = 'none'
  const root: Root = createRoot(dom)
  let wpos: { line: number; column: number } | null = null
  let visible = false
  let current: { expr: string; token: number } | null = null
  let seq = 0
  const cache = new Map<string, { v?: string; e?: string }>()
  // fgldb 报 No symbol 的词 = 不是变量:负缓存,悬浮直接略过(步进/换帧后清空——
  // 同名符号在不同函数作用域可能存在)
  const noSymbol = new Set<string>()

  const widget: monaco.editor.IContentWidget = {
    getId: () => 'fgl.hover.card',
    getDomNode: () => dom,
    getPosition: () => {
      if (!wpos) return null
      const lineCount = editor.getModel()?.getLineCount() ?? 1
      return {
        position: { lineNumber: Math.min(wpos.line + 1, lineCount), column: wpos.column },
        preference: [monaco.editor.ContentWidgetPositionPreference.EXACT],
      }
    },
  }
  editor.addContentWidget(widget)

  // 滚动接管:卡片内容可滚动时,滚轮只滚卡片、不冒泡给编辑器(否则长值滚动时代码跟着滚);
  // 卡片内容不滚动时放行,滚轮仍滚代码
  dom.addEventListener('wheel', (e) => {
    const body = dom.querySelector('.fgl-hover-body')
    if (body && body.scrollHeight > body.clientHeight) e.stopPropagation()
  })

  const render = (d: { v?: string; e?: string }) => root.render(<HoverCard {...d} />)
  // 真正弹卡:只在拿到求值结果后调用
  const present = (expr: string, pos: monaco.Position, data: { v?: string; e?: string }) => {
    wpos = { line: pos.lineNumber, column: pos.column }
    current = { expr, token: ++seq }
    dom.style.display = 'block'
    visible = true
    render({ v: data.v, e: data.e })
    editor.layoutContentWidget(widget)
  }
  // 驻留到期:缓存命中直接弹卡;否则先静默求值,拿到结果才弹——
  // No symbol(非变量)永不弹卡,其余错误以错误态弹出
  const dwell = (expr: string, pos: monaco.Position) => {
    const cached = cache.get(expr)
    if (cached) { present(expr, pos, cached); return }
    const token = ++seq
    current = { expr, token } // 占位:等待结果期间同词不重触发(卡片未显示)
    const st = useStore.getState()
    api.print(st.sessionId!, expr)
      .then(({ value }) => {
        cache.set(expr, { v: value })
        if (current?.token === token) present(expr, pos, { v: value })
      })
      .catch((err: Error) => {
        if (/no symbol/i.test(err.message)) {
          noSymbol.add(expr)
          if (current?.token === token) current = null // 静默放弃,从未显示过
          return
        }
        cache.set(expr, { e: err.message })
        if (current?.token === token) present(expr, pos, { e: err.message })
      })
  }
  // 悬停驻留延时:同一变量上停稳 500ms 才发卡并求值,快速扫过不触发
  const HOVER_DELAY_MS = 500
  let pending: { expr: string; pos: monaco.Position; timer: number } | null = null
  const cancelPending = () => {
    if (pending) {
      window.clearTimeout(pending.timer)
      pending = null
    }
  }
  const hide = () => {
    cancelPending()
    current = null // 求值占位一并作废(结果回来后不再弹卡)
    if (!visible) return
    visible = false
    wpos = null
    dom.style.display = 'none'
    editor.layoutContentWidget(widget)
  }
  // 鼠标在卡片内(含 8px 缓冲)时不隐藏——移向卡片点击复制/展开树途中不被判为换词
  const inCard = (mx: number, my: number) => {
    if (!visible) return false
    const r = dom.getBoundingClientRect()
    return mx >= r.left - 8 && mx <= r.right + 8 && my >= r.top - 8 && my <= r.bottom + 8
  }
  const hideIfOutside = (mx: number, my: number) => {
    if (visible && !inCard(mx, my)) hide()
  }

  editor.onMouseMove((e) => {
    const mx = e.event.posx
    const my = e.event.posy
    if (e.target.type !== monaco.editor.MouseTargetType.CONTENT_TEXT) { hideIfOutside(mx, my); return }
    const model = editor.getModel()
    if (!model || model.uri.scheme !== 'debug') { hide(); return }
    const st = useStore.getState()
    if (!st.sessionId || st.state !== 'stopped') { hide(); return }
    const pos = e.target.position
    if (!pos) { hideIfOutside(mx, my); return }
    const expr = exprAt(model, pos)
    if (!expr) { hideIfOutside(mx, my); return }
    if (noSymbol.has(expr)) { hide(); return } // 已知非变量,静默略过
    if (current?.expr === expr) return // 已展示或求值进行中(同词不重触发)
    if (visible && inCard(mx, my)) return // 移向卡片途中不切换
    if (pending?.expr === expr) return // 已在驻留等待中,不重复计时
    cancelPending()
    hide()
    pending = {
      expr,
      pos,
      timer: window.setTimeout(() => {
        pending = null
        dwell(expr, pos)
      }, HOVER_DELAY_MS),
    }
  })
  editor.onMouseLeave(() => hide())
  // 兜底:鼠标移出编辑器区域(右侧面板/顶栏等)也收卡——不完全依赖 Monaco 的 leave 事件
  const onDocMove = (ev: MouseEvent) => {
    if (!current) return // 无卡片也无私下求值时无需处理
    const edom = editor.getDomNode()
    if (!edom) return
    if (edom.contains(ev.target as Node)) return // 编辑器内部交给 editor.onMouseMove
    if (inCard(ev.clientX, ev.clientY)) return // 移向卡片途中
    hide()
  }
  document.addEventListener('mousemove', onDocMove)
  editor.onMouseDown((e) => {
    if (e.target.type !== monaco.editor.MouseTargetType.CONTENT_TEXT) hide()
  })
  editor.onKeyDown((e) => { if (e.keyCode === monaco.KeyCode.Escape) hide() })
  editor.onDidChangeModel(() => hide())
  // 离开停站即收卡;停站行变化(步进/换帧)清缓存与负缓存,保证下次悬浮取到新值
  // (同名符号在别的函数作用域可能是变量)
  let lastStop = ''
  useStore.subscribe((s) => {
    if (s.state !== 'stopped') hide()
    const k = s.stop ? `${s.stop.file}:${s.stop.line}` : ''
    if (k !== lastStop) {
      lastStop = k
      cache.clear()
      noSymbol.clear()
    }
  })

  // ---- 右键菜单:取值复制(复用悬浮的 cache / noSymbol / print 链路) ----
  const st = () => useStore.getState()
  const timeline = (text: string, kind: 'info' | 'warn' = 'info') =>
    st().pushTimeline({ origin: 'human', kind, text })

  // 取一个表达式:cache 命中直接用;否则 print(No symbol 记负缓存并抛标记)
  const evalExpr = async (expr: string): Promise<string> => {
    const cached = cache.get(expr)
    if (cached?.v !== undefined) return cached.v
    try {
      const { value } = await api.print(st().sessionId!, expr)
      cache.set(expr, { v: value })
      return value
    } catch (err: any) {
      if (/no symbol/i.test(err.message)) noSymbol.add(expr)
      throw err
    }
  }

  // 「取值后复制」:选中区域内标识符逐个取值,解析成功的替换进文本(双引号串转单引号),
  // 结果整体写剪贴板——用于把代码里的 SQL 抠出来直接可用
  editor.addAction({
    id: 'fgl.copyWithValues',
    label: '取值后复制(变量替换为值)',
    contextMenuGroupId: 'fgl',
    contextMenuOrder: 1,
    run: async (ed) => {
      const sel = ed.getSelection()
      const text = sel ? ed.getModel()?.getValueInRange(sel) || '' : ''
      if (!text.trim()) { timeline('取值后复制:请先选中要处理的文本(如 SQL)', 'warn'); return }
      if (st().state !== 'stopped') { timeline('取值后复制:仅停站时可取值', 'warn'); return }
      const vars = candidateVars(text)
      if (!vars.length) { timeline('取值后复制:选中内容里没有可尝试的变量', 'warn'); return }
      const repl: [string, string][] = []
      for (const v of vars) {
        if (noSymbol.has(v)) continue
        try {
          repl.push([v, valueForSql(await evalExpr(v))])
        } catch { /* No symbol 或取值失败:原样保留 */ }
      }
      if (!repl.length) { timeline('取值后复制:选中内容中的名称都不是可取值变量', 'warn'); return }
      let out = text
      for (const [name, val] of repl.sort((a, b) => b[0].length - a[0].length)) {
        out = out.replace(new RegExp(`\\b${name.replace(/\./g, '\\.')}\\b`, 'g'), val)
      }
      await writeClipboard(out)
      timeline(`取值后复制:已替换 ${repl.length}/${vars.length} 个变量并复制(${repl.map(([n]) => n).join(', ')})`)
    },
  })

  // 「复制值」:对选中文本或光标处表达式执行一次取值,原始值写剪贴板
  editor.addAction({
    id: 'fgl.copyValue',
    label: '复制值',
    contextMenuGroupId: 'fgl',
    contextMenuOrder: 2,
    run: async (ed) => {
      const sel = ed.getSelection()
      const selText = sel ? ed.getModel()?.getValueInRange(sel) || '' : ''
      const expr = selText.trim() || (() => {
        const model = ed.getModel()
        const pos = ed.getPosition()
        return model && pos ? exprAt(model, pos) : null
      })()
      if (!expr?.trim()) { timeline('复制值:请先把光标放在变量上或选中表达式', 'warn'); return }
      if (st().state !== 'stopped') { timeline(`复制值:仅停站时可取值(${expr})`, 'warn'); return }
      try {
        const value = await evalExpr(expr.trim())
        await writeClipboard(value)
        timeline(`复制值 ${expr} → ${value.length > 120 ? value.slice(0, 120) + '…' : value}`)
      } catch (e: any) {
        timeline(`复制值 ${expr} 失败: ${/no symbol/i.test(e.message || '') ? '不是可取值变量' : e.message}`, 'warn')
      }
    },
  })

  // 「运行到光标处」:停站时从当前停站位置继续执行到光标所在行(fgldb until,同 VS Code)
  editor.addAction({
    id: 'fgl.runToCursor',
    label: '运行到光标处',
    contextMenuGroupId: 'fgl',
    contextMenuOrder: 3,
    run: async (ed) => {
      const pos = ed.getPosition()
      if (!pos) return
      const s = st()
      if (!s.sessionId) { timeline('运行到光标处:无调试会话', 'warn'); return }
      if (s.state !== 'stopped') { timeline('运行到光标处:仅停站时可用', 'warn'); return }
      await s.runToCursor(pos.lineNumber)
    },
  })
}
