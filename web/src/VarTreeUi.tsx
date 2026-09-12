// 纯前端变量树渲染:children 已在内存(如 fgldb print 文本解析出的 RECORD/ARRAY),
// 展开收起只是 UI 折叠,无后端交互。样式与 dap 分支的变量树一致。
import { useState } from 'react'
import type { TNode } from './fglparse'

function NodeRow({ node, depth, toggle }: { node: TNode; depth: number; toggle: (n: TNode) => void }) {
  const expandable = !!node.children?.length
  return (
    <>
      <div
        className={`flex items-baseline gap-1 px-1 hover:bg-foreground/5 ${expandable ? 'cursor-pointer' : 'cursor-default'}`}
        style={{ paddingLeft: depth * 14 + 4 }}
        onClick={() => expandable && toggle(node)}
      >
        <span className={`w-3 shrink-0 text-muted-foreground ${expandable ? '' : 'opacity-0'}`}>
          {node.loading ? '…' : node.open ? '▾' : '▸'}
        </span>
        <span className="text-sky-700 dark:text-sky-300 whitespace-pre">{node.name}</span>
        {node.value !== undefined && (
          <>
            <span className="text-muted-foreground whitespace-pre"> = </span>
            <span className="text-foreground break-all">{node.value || '(空)'}</span>
          </>
        )}
        {node.type && <span className="ml-1 shrink-0 text-muted-foreground">{node.type}</span>}
      </div>
      {node.open && node.children?.map((c, j) => (
        <NodeRow key={j} node={c} depth={depth + 1} toggle={toggle} />
      ))}
    </>
  )
}

// 展示一组已构建好的节点(顶层各自带展开状态)
export function VarTreeNodes({ nodes }: { nodes: TNode[] }) {
  const [, bump] = useState(0)
  const toggle = (n: TNode) => {
    if (!n.children?.length) return
    n.open = !n.open
    bump((v) => v + 1)
  }
  return (
    <div className="text-xs overflow-auto">
      {nodes.map((n, i) => (
        <NodeRow key={i} node={n} depth={0} toggle={toggle} />
      ))}
    </div>
  )
}
