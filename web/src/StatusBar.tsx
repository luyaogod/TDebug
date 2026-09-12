// 底部状态栏:整条背景色表达调试状态(VS Code 风格:停站橙 / 运行蓝 / 启动灰),单行紧凑布局
import { useStore } from './store'

export function StatusBar() {
  const sourcePath = useStore((s) => s.sourcePath)
  const prog = useStore((s) => s.prog)
  const state = useStore((s) => s.state)
  const stop = useStore((s) => s.stop)
  const hold = useStore((s) => s.holdingSeconds)
  const sessionEnv = useStore((s) => s.sessionEnv)

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
    msg = '运行中 · 请到 GDC 操作'
    title = '程序运行中,请在 GDC 操作作业界面'
  } else if (state === 'loading') {
    bar = 'bg-accent text-foreground'
    msg = '启动中…'
  } else if (state === 'idle') {
    // 本轮调试结束,宿主会话保留(idle):再次启动/换作业免重新登录
    bar = 'bg-foreground/10 text-foreground'
    msg = sessionEnv ? `会话空闲(${sessionEnv}) · 可直接启动调试` : '会话空闲 · 可直接启动调试'
    title = '会话保留中(SSH/登录态未断开),可直接启动新一轮调试'
  }

  return (
    <div className={`flex h-7 shrink-0 items-center gap-3 border-t border-border px-3 text-[11px] transition-colors ${bar}`}>
      {/* 左:源码文件路径 */}
      <span className="min-w-0 flex-1 truncate font-mono" title={sourcePath || ''}>
        {sourcePath || ' '}
      </span>
      {/* 右:会话状态(原胶囊文字) + 当前作业(仅作业号) */}
      <span className="flex shrink-0 items-center gap-3">
        {msg && <span title={title}>{msg}</span>}
        {state && prog && <span className="font-medium" title={`作业 ${prog}`}>{prog}</span>}
      </span>
    </div>
  )
}
