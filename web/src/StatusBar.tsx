// 底部状态栏:整条背景色表达调试状态(VS Code 风格:停站橙 / 运行蓝 / 启动灰),单行紧凑布局
// 协作模式下额外承载"谁在驾驶 / AI 正在执行什么 / 接管按钮"。
import { useEffect, useState } from 'react'
import { useStore } from './store'

export function StatusBar() {
  const sourcePath = useStore((s) => s.sourcePath)
  const prog = useStore((s) => s.prog)
  const state = useStore((s) => s.state)
  const stop = useStore((s) => s.stop)
  const hold = useStore((s) => s.holdingSeconds)
  const sessionEnv = useStore((s) => s.sessionEnv)
  const sessionId = useStore((s) => s.sessionId)
  const mode = useStore((s) => s.mode)
  const inflight = useStore((s) => s.inflight)
  const setMode = useStore((s) => s.setMode)
  const collab = mode === 'collab'

  // inflight 的 elapsed 是快照那一刻的读数,长命令(continue)会停在那儿不动 ——
  // 用 since 在本地每秒重算一次,界面才是"正在跑"而不是"卡住了"。
  const [, tick] = useState(0)
  useEffect(() => {
    if (!inflight) return
    const t = window.setInterval(() => tick((n) => n + 1), 1000)
    return () => clearInterval(t)
  }, [inflight])
  const inflightSecs = inflight ? Math.max(0, Math.round((Date.now() - Date.parse(inflight.since)) / 1000)) : 0

  let bar = 'bg-background text-muted-foreground'
  let msg: string | null = null
  let title = ''
  if (state === 'stopped') {
    const loc = stop?.file ? `${stop.file}:${stop.line}` : ''
    bar = 'bg-[#cc6633] text-white'
    msg = `已停站${loc ? ` ${loc}` : ''} · 停留 ${Math.round(hold)}s`
    title = '程序已暂停,可查看变量/下断点/继续'
  } else if (state === 'running') {
    bar = 'bg-[#0078d4] text-white'
    msg = collab ? 'AI 已放行 · 在界面上观察即可' : '运行中 · 请到 GDC 操作'
    title = collab ? '程序运行中;协作模式下由 AI 主导,你在界面上只能观察与取值' : '程序运行中,请在 GDC 操作作业界面'
  } else if (state === 'loading') {
    bar = 'bg-accent text-foreground'
    msg = '启动中…'
  } else if (state === 'idle') {
    // 本轮调试结束,宿主会话保留(idle):再次启动/换作业免重新登录
    bar = 'bg-foreground/10 text-foreground'
    msg = sessionEnv ? `会话空闲(${sessionEnv}) · 可直接启动调试` : '会话空闲 · 可直接启动调试'
    title = '会话保留中(SSH/登录态未断开),可直接启动新一轮调试'
  }

  const chip = 'shrink-0 rounded-sm bg-black/20 px-1.5 py-px'

  return (
    <div className={`flex h-7 shrink-0 items-center gap-3 border-t border-border px-3 text-[11px] transition-colors ${bar}`}>
      {/* 左:源码文件路径 */}
      <span className="min-w-0 flex-1 truncate font-mono" title={sourcePath || ''}>
        {sourcePath || ' '}
      </span>
      {/* 右:协作模式标识 + 在飞命令 + 会话状态 + 当前作业 */}
      <span className="flex shrink-0 items-center gap-2">
        {collab && (
          <span className={chip} title="本次调试由 AI 主导:你只能观察与取值;需要下断点或执行命令,请在对话里委托给 AI">
            协作模式 · AI 主导
          </span>
        )}
        {inflight && (
          <span className={`${chip} font-mono`} title="正在执行的调试命令">
            ⟳ {inflight.cmd}(已 {inflightSecs}s)
          </span>
        )}
        {sessionId && (
          <button
            className="shrink-0 rounded-sm border border-current/40 px-1.5 py-px opacity-80 hover:bg-black/10 hover:opacity-100"
            onClick={() => void setMode(collab ? 'solo' : 'collab')}
            title={collab ? '切回纯人工:界面恢复全部写操作' : '把本次调试交给 AI 主导:你转为只读观察'}
          >
            {collab ? '接管' : '交给 AI'}
          </button>
        )}
        {msg && <span title={title}>{msg}</span>}
        {state && prog && <span className="font-medium" title={`作业 ${prog}`}>{prog}</span>}
      </span>
    </div>
  )
}
