// 大纲面板:解析当前显示的 4GL 源码(FUNCTION/MAIN + DIALOG/CONSTRUCT/INPUT/INPUT ARRAY 及其子块),
// 树形展示并点击跳行。跳转直接操作编辑器视口(setPosition + revealLineInCenterIfOutsideViewport),
// 不走 revealReq——不受"仅停站可定位"的调试门限制,运行中也可浏览跳转,且不产生任何调试信号。
// 调试页行号需补偿 lineOffset(Monaco 顶部前插空行对齐 DVM 行号)。
// 高亮跟随编辑器光标:定位光标所在的最深层节点;若其上级被折叠,则高亮可见的那个祖先节点,
// 展开后再落到具体层级(不自动展开)。
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ChevronsDownUp, ChevronsUpDown } from 'lucide-react'
import { useStore } from './store'
import { editorRef } from './SourceView'
import { parseOutline, type OutlineNode } from './fgloutline'

// 节点 key 由 path+行+label 决定,行渲染、折叠集合、全量收集共用同一算法
const nodeKey = (node: OutlineNode, path: string) => `${path}/${node.line}:${node.label}`

// 带文档序索引的节点:end = 大纲覆盖到的源码行(含)= 先序遍历中本子树之后下一个节点的 line-1
interface IndexedNode {
  label: string
  line: number
  ord: number
  sub: number // 子树节点总数(含自身)
  end: number
  children?: IndexedNode[]
}

function indexTree(roots: OutlineNode[], totalLines: number): { rooted: IndexedNode[]; list: IndexedNode[] } {
  const list: IndexedNode[] = []
  const walk = (nodes: OutlineNode[]): IndexedNode[] =>
    nodes.map((n) => {
      const node: IndexedNode = { label: n.label, line: n.line, ord: list.length, sub: 1, end: 0 }
      list.push(node)
      const kids = n.children ? walk(n.children) : undefined
      node.children = kids
      node.sub = 1 + (kids ? kids.reduce((s, k) => s + k.sub, 0) : 0)
      return node
    })
  const rooted = walk(roots)
  for (const node of list) {
    const next = list[node.ord + node.sub]
    node.end = (next ? next.line : totalLines + 1) - 1
  }
  return { rooted, list }
}

// 光标行 → 从根到最深包含节点的链(区间互斥且按行有序,逐层线性扫描即可)
const findChain = (nodes: IndexedNode[], line: number, path: string, acc: { node: IndexedNode; key: string }[]) => {
  for (const n of nodes) {
    if (line < n.line) break
    if (line <= n.end) {
      const key = nodeKey(n, path)
      acc.push({ node: n, key })
      if (n.children) findChain(n.children, line, key, acc)
      return
    }
  }
}

const collectKeys = (nodes: IndexedNode[], path: string, acc: string[]) => {
  for (const n of nodes) {
    const key = nodeKey(n, path)
    if (n.children && n.children.length) {
      acc.push(key)
      collectKeys(n.children, key, acc)
    }
  }
}

export function OutlinePanel() {
  const sourceContent = useStore((s) => s.sourceContent)
  const lineOffset = useStore((s) => s.lineOffset)
  const tabs = useStore((s) => s.tabs)
  const activeTab = useStore((s) => s.activeTab)
  const active = tabs.find((t) => t.key === activeTab)
  const isDebug = !active
  const content = isDebug ? (sourceContent || '') : (active!.content || '')
  const offset = isDebug ? lineOffset : 0
  const totalLines = useMemo(() => content.split('\n').length, [content])
  const { rooted } = useMemo(() => indexTree(parseOutline(content), totalLines), [content, totalLines])
  // 折叠集合:默认全展开;key 与渲染行共用 nodeKey 算法
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const allKeys = useMemo(() => {
    const acc: string[] = []
    collectKeys(rooted, '', acc)
    return acc
  }, [rooted])
  const allExpanded = collapsed.size === 0
  const toggleAll = () => setCollapsed(allExpanded ? new Set(allKeys) : new Set())

  // 光标行跟踪:编辑器可能在面板挂载后才就绪,轮询挂接一次
  const [cursorLine, setCursorLine] = useState(0)
  useEffect(() => {
    const disp: { dispose(): void }[] = []
    let timer: number | undefined
    const attach = () => {
      const ed = editorRef.current
      if (!ed) return false
      const sync = () => {
        const p = ed.getPosition()
        setCursorLine(p ? p.lineNumber : 0)
      }
      disp.push(ed.onDidChangeCursorPosition(sync))
      disp.push(ed.onDidChangeModel(sync))
      sync()
      return true
    }
    if (!attach()) timer = window.setInterval(() => { if (attach()) window.clearInterval(timer) }, 200)
    return () => {
      if (timer) window.clearInterval(timer)
      disp.forEach((d) => d.dispose())
    }
  }, [])

  // 光标 → 源码行 → 节点链 → 可见高亮节点(沿链向下,遇到折叠的祖先即停)
  const cursorSrc = cursorLine - offset
  const visibleKey = useMemo(() => {
    if (cursorSrc <= 0) return ''
    const chain: { node: IndexedNode; key: string }[] = []
    findChain(rooted, cursorSrc, '', chain)
    if (!chain.length) return ''
    let key = chain[0].key
    for (let i = 1; i < chain.length; i++) {
      if (collapsed.has(chain[i - 1].key)) break
      key = chain[i].key
    }
    return key
  }, [rooted, cursorSrc, collapsed])

  // 高亮行滚动进可视区(不抢列表滚动位置)
  const hlRef = useRef<HTMLDivElement | null>(null)
  useEffect(() => {
    hlRef.current?.scrollIntoView({ block: 'nearest' })
  }, [visibleKey])

  const jump = (line: number) => {
    const ed = editorRef.current
    if (!ed) return
    const L = line + offset
    ed.setPosition({ lineNumber: L, column: 1 })
    ed.revealLineInCenterIfOutsideViewport(L)
  }
  const toggleKey = (key: string) => {
    setCollapsed((prev) => {
      const n = new Set(prev)
      if (n.has(key)) n.delete(key)
      else n.add(key)
      return n
    })
  }

  const row = (node: IndexedNode, depth: number, path: string): ReactNode => {
    const key = nodeKey(node, path)
    const kids = node.children
    const hasKids = !!kids && kids.length > 0
    const isCollapsed = collapsed.has(key)
    const hl = key === visibleKey
    return (
      <div key={key}>
        <div ref={hl ? hlRef : undefined} onClick={() => jump(node.line)}
          className={`flex h-6 cursor-pointer items-center gap-1 pr-2 text-xs ${
            hl ? 'bg-accent/70 text-foreground' : 'hover:bg-accent/40'
          } ${hl ? '' : depth === 0 ? 'font-medium text-foreground' : depth === 1 ? 'text-sky-600 dark:text-sky-400' : 'text-muted-foreground'}`}
          style={{ paddingLeft: depth * 14 + 4 }}
          title={`${node.label} (第 ${node.line} 行)`}>
          {hasKids ? (
            <button className="w-3 shrink-0 text-muted-foreground"
              title={isCollapsed ? '展开' : '收起'}
              onClick={(e) => { e.stopPropagation(); toggleKey(key) }}>
              {isCollapsed ? '▸' : '▾'}
            </button>
          ) : (
            <span className="w-3 shrink-0" />
          )}
          <span className="min-w-0 truncate">{node.label}</span>
        </div>
        {hasKids && !isCollapsed && kids!.map((k) => row(k, depth + 1, key))}
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 w-full flex-col overflow-hidden bg-background">
      <div className="flex h-8 shrink-0 items-center justify-between border-b border-border px-2.5">
        <span className="text-xs font-medium text-muted-foreground">大纲</span>
        <div className="flex items-center gap-1.5">
          <span className="text-[11px] text-muted-foreground">{rooted.length} 个函数</span>
          <button title={allExpanded ? '全部折叠' : '全部展开'} onClick={toggleAll}
            disabled={allKeys.length === 0}
            className="p-0.5 text-muted-foreground transition-colors hover:text-foreground disabled:pointer-events-none disabled:opacity-40">
            {allExpanded ? <ChevronsDownUp className="h-3.5 w-3.5" /> : <ChevronsUpDown className="h-3.5 w-3.5" />}
          </button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-auto py-1">
        {rooted.length === 0 && (
          <div className="p-2 text-xs text-muted-foreground">当前源码无可识别的函数/交互块</div>
        )}
        {rooted.map((n) => row(n, 0, ''))}
      </div>
    </div>
  )
}
