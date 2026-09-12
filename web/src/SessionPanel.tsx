// 「会话」sheet:管理全局单一常驻会话(每个环境一条,通常就是默认环境那一条)。
// 会话 = 与某 T100 环境的 SSH+登录态,debug 只是在其上跑一轮;结束调试/程序退出都只回空闲。
// 本面板负责 切换(换到另一环境并重连)/ 重启(断开重连同环境)/ 结束(彻底释放连接);
// 这些操作都会先结束当前 debug(由后端收口)。行只显示环境名 + 状态,保持精简。
// 两个可折叠区域(仿 debug 运行区手风琴):「会话」(环境行列表)、
// 「环境变量」(会话 TOPENT:连接后带出登录回读的实际值供参考,空闲时可覆盖)。
import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { ArrowRightLeft, Check, RefreshCw, RotateCcw, Power } from 'lucide-react'
import { useStore } from './store'
import { api } from './api'
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, Input } from './ui'

const STATE_LABEL: Record<string, string> = {
  idle: '空闲', loading: '连接中…', stopped: '已停站', running: '运行中', exit: '已断开',
}
const STATE_DOT: Record<string, string> = {
  idle: 'bg-emerald-500',
  loading: 'bg-amber-400',
  stopped: 'bg-[#cc6633]',
  running: 'bg-[#0078d4]',
  exit: 'bg-muted-foreground/50',
}

// 折叠区头(仿 RightPanels 手风琴):标题(可点击)…附加控件…折叠箭头;
// topLine 为真时块顶加分割线(与会话内容分隔,避免与上一块底边线叠成双线)
function SectionHead({ title, open, onToggle, topLine, extra }: {
  title: string; open: boolean; onToggle: () => void; topLine?: boolean; extra?: ReactNode
}) {
  const tip = open ? `收起${title}` : `展开${title}`
  return (
    <div className={`flex h-8 shrink-0 items-center border-b border-border px-2.5 text-xs font-medium text-muted-foreground ${
      topLine ? 'border-t border-t-border' : ''
    }`}>
      <button onClick={onToggle} title={tip}
        className="min-w-0 flex-1 truncate text-left hover:text-foreground">
        {title}
      </button>
      {extra}
      <button onClick={onToggle} title={tip}
        className="p-0.5 text-muted-foreground transition-colors hover:text-foreground">
        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none"
          stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
          className={`shrink-0 text-muted-foreground transition-transform duration-200 ${open ? 'rotate-180' : ''}`}>
          <path d="m6 9 6 6 6-6" />
        </svg>
      </button>
    </div>
  )
}

export function SessionPanel() {
  const sessionId = useStore((s) => s.sessionId)
  const sessionEnv = useStore((s) => s.sessionEnv)
  const state = useStore((s) => s.state)
  const sessionOp = useStore((s) => s.sessionOp)

  const [envNames, setEnvNames] = useState<string[]>([])
  const [busy, setBusy] = useState<string | null>(null)
  const [err, setErr] = useState('')

  // 两个可折叠区域(默认展开,仿 debug 运行区)
  const [open, setOpen] = useState({ session: true, env: true })
  const toggle = (k: 'session' | 'env') => setOpen((o) => ({ ...o, [k]: !o[k] }))

  // 环境清单来自设置(名称即会话目标);当前环境由会话本身决定(选环境 = 连会话)
  const load = useCallback(() => {
    void api.settings().then((c: any) => {
      const names = (c.sshs || [])
        .map((e: any) => e.name || `${e.host || ''}-${e.zone || ''}`.replace(/-$/, ''))
        .filter(Boolean)
      setEnvNames(names)
    }).catch(() => {})
  }, [])
  useEffect(() => { load() }, [load])
  // 连接中 loading→idle 等状态跟随;每 3s 轻量刷新一次
  useEffect(() => {
    const t = window.setInterval(load, 3000)
    return () => window.clearInterval(t)
  }, [load])

  // 会话所属环境:直接取会话快照里的环境名(不再有"默认环境"这种配置项)
  const curEnv = sessionEnv || ''
  // 行 = 已配置环境;当前会话不在列表(顶层直连等)时补一行。
  // 圆点只表示"是否连着会话":未连接一律灰点,连上的那行才按状态上色(空闲绿/运行蓝/停站橙)。
  const connectedEnv = sessionId && curEnv ? [curEnv] : []
  const rows = Array.from(new Set([...envNames, ...(curEnv ? [curEnv] : [])]))

  // 会话操作:有副作用的先弹 shadcn 二次确认(替代浏览器 confirm),确认后再执行
  const [pending, setPending] = useState<{ op: 'close' | 'restart' | 'switch'; env?: string; title: string; desc: string } | null>(null)

  const runOp = async (op: 'close' | 'restart' | 'switch', env?: string) => {
    setBusy(`${op}:${env || ''}`)
    setErr('')
    try {
      await sessionOp(op, env)
    } catch (e: any) {
      setErr(e.message || String(e))
    } finally {
      setBusy(null)
    }
  }

  const doOp = (op: 'close' | 'restart' | 'switch', env?: string) => {
    const hasRun = !!sessionId && state !== 'idle' && state !== '' && state !== 'exit'
    const envLabel = env || curEnv || ''
    if (op === 'close') {
      setPending({ op, env, title: `结束会话「${envLabel}」?`, desc: '将断开该环境的 SSH 连接并结束当前调试。' })
    } else if (op === 'restart') {
      setPending({ op, env, title: `重启会话「${envLabel}」?`, desc: '将断开并重新连接该环境,会先结束当前调试。' })
    } else if (op === 'switch' && hasRun) {
      setPending({ op, env, title: `切换会话到「${envLabel}」?`, desc: '会先结束当前调试并断开当前连接。' })
    } else {
      void runOp(op, env) // 空闲态切换无需确认
    }
  }

  const confirmPending = () => {
    const p = pending
    setPending(null)
    if (p) void runOp(p.op, p.env)
  }

  // ---- 环境变量:TOPENT(会话内,仅 idle 可设置) ----
  // topent = 会话内手动覆盖(空 = 未覆盖);topentShell = 当前会话实际值(后端在连接时
  // 已把环境默认 TOPENT 下发到 shell,并同步回读值);topentCfg = 环境配置值。
  // 未覆盖时把实际值直接显示在输入框里,让用户一眼看到当前连的是哪个 TOPENT,
  // 需要时再手动改并保存。
  const [topent, setTopent] = useState('')
  const [topentShell, setTopentShell] = useState('')
  const [topentCfg, setTopentCfg] = useState('')
  const [topentSaved, setTopentSaved] = useState(false)
  const [topentErr, setTopentErr] = useState('')
  const canSetTopent = !!sessionId && state === 'idle' && !busy
  // 依赖里必须带 state:会话先建好(sessionId 先到、state=loading),登录与 TOPENT 下发
  // 完成后才有值;只在 sessionId 变化时取会拿到登录前的空值,面板就一直是空的。
  const loadTopent = useCallback(() => {
    if (!sessionId) { setTopent(''); setTopentShell(''); setTopentCfg(''); return }
    void api.snapshot(sessionId).then((snap: any) => {
      setTopent(snap.topent || '')
      setTopentShell(snap.topentShell || '')
      setTopentCfg(snap.topentCfg || '')
    }).catch(() => {})
  }, [sessionId, state])
  useEffect(() => { loadTopent() }, [loadTopent])
  // 输入框显示值:手动覆盖 > 登录回读的实际值 > 环境配置值(逐级兜底,避免出现空框)
  const shownTopent = topent || topentShell || topentCfg
  const saveTopent = async () => {
    if (!canSetTopent || !sessionId) return
    // 不限数字/文本:仅剔除两侧空白,留空 = 清除手动设置
    const v = topent.trim()
    setTopent(v)
    setTopentErr('')
    try {
      await api.topent(sessionId, v)
      setTopentSaved(true)
      useStore.getState().pushTimeline({ origin: 'human', kind: 'command', text: `设置 TOPENT${v ? '=' + v : '(清除)'}` })
      window.setTimeout(() => setTopentSaved(false), 1500)
    } catch (e: any) {
      setTopentErr(e.message || String(e))
    }
  }

  return (
    <div className="flex h-full min-h-0 w-full flex-col overflow-hidden bg-background">
      <div className="min-h-0 flex-1 overflow-auto">
        {/* 区域一:会话(环境行列表) */}
        <SectionHead title="会话" open={open.session} onToggle={() => toggle('session')}
          extra={
            <button title="刷新环境/会话状态" onClick={load}
              className="ml-1 p-0.5 text-muted-foreground transition-colors hover:text-foreground">
              <RefreshCw className="h-3.5 w-3.5" />
            </button>
          } />
        {open.session && (
          <div className="py-1">
            {rows.length === 0 && (
              <div className="space-y-1 p-2 text-xs text-muted-foreground">
                <div>尚未配置环境(设置 → 环境)。</div>
                <div>直接「启动调试」将使用默认连接并自动建立会话。</div>
              </div>
            )}
            {rows.map((name) => {
              const connected = connectedEnv.length > 0 && name === curEnv
              const st = connected ? (state || '') : ''
              return (
                <div key={name}
                  className={`group flex h-7 items-center gap-1.5 px-2.5 text-xs ${
                    connected ? 'bg-accent/40' : 'hover:bg-accent/30'
                  }`}
                  title={connected ? `当前会话 · ${STATE_LABEL[st] || st}` : `环境 ${name}`}>
                  <span className={`h-2 w-2 shrink-0 rounded-full ${connected ? (STATE_DOT[st] || 'bg-accent') : 'bg-muted-foreground/25'}`} />
                  <span className="min-w-0 flex-1 truncate font-medium">{name}</span>
                  {connected && (
                    <span className="shrink-0 text-[11px] text-muted-foreground">{STATE_LABEL[st] || st}</span>
                  )}
                  {connected ? (
                    <span className="flex shrink-0 items-center gap-0.5 opacity-70 group-hover:opacity-100">
                      <button title="重启会话(断开重连同环境)" disabled={!!busy}
                        onClick={() => void doOp('restart', name)}
                        className="p-1 text-muted-foreground transition-colors hover:text-foreground disabled:pointer-events-none disabled:opacity-30">
                        <RotateCcw className="h-3.5 w-3.5" />
                      </button>
                      <button title="结束会话(彻底断开连接)" disabled={!!busy}
                        onClick={() => void doOp('close')}
                        className="p-1 text-muted-foreground transition-colors hover:text-red-600 dark:hover:text-red-400 disabled:pointer-events-none disabled:opacity-30">
                        <Power className="h-3.5 w-3.5" />
                      </button>
                    </span>
                  ) : (
                    <span className="flex shrink-0 items-center gap-0.5 opacity-70 group-hover:opacity-100">
                      <button title={`切换会话到 ${name}(结束当前 debug 并连接该环境)`} disabled={!!busy}
                        onClick={() => void doOp('switch', name)}
                        className="p-1 text-muted-foreground transition-colors hover:text-foreground disabled:pointer-events-none disabled:opacity-30">
                        <ArrowRightLeft className="h-3.5 w-3.5" />
                      </button>
                    </span>
                  )}
                </div>
              )
            })}
            {err && <div className="px-2.5 pb-1 text-[11px] text-red-600 dark:text-red-400">{err}</div>}
          </div>
        )}

        {/* 区域二:环境变量(TOPENT 设置,空闲会话可改);会话展开时块顶加分割线 */}
        <SectionHead title="环境变量" open={open.env} onToggle={() => toggle('env')} topLine={open.session} />
        {open.env && (
          <div className="py-1">
            <div className="flex items-center gap-1.5 px-2 py-0.5">
              <span className="w-[52px] shrink-0 text-xs text-muted-foreground"
                title="TOPENT(企业编号):T100 运行时的企业环境,作业运行与数据库连接以此为准">TOPENT</span>
              <Input value={shownTopent} disabled={!canSetTopent}
                onChange={(e) => setTopent(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') void saveTopent() }}
                title="会话 TOPENT:未覆盖时显示当前会话实际值(连接时已按环境配置下发);改动后保存 = 设为会话覆盖,留空保存 = 清除覆盖(回环境默认)"
                className="h-6 min-w-0 flex-1 px-1.5 font-mono text-xs" />
              <button disabled={!canSetTopent} onClick={() => void saveTopent()}
                title="保存 TOPENT(立即下发到当前会话)"
                className={`flex h-6 shrink-0 items-center px-1.5 text-xs transition-colors disabled:pointer-events-none disabled:opacity-30 ${
                  topentSaved ? 'text-emerald-600 dark:text-emerald-400' : 'hover:bg-accent/60'
                }`}>
                {topentSaved ? <Check className="h-3.5 w-3.5" /> : '保存'}
              </button>
            </div>
            {/* 标注显示值的来源:手动覆盖 / 连接时按配置下发的实际值 / 配置值兜底。
                配置是默认值,连接时已下发;用户后续改配置则下一轮调试生效 —— 属正常行为,
                不用告警色,只在两者确实不同时说一句。 */}
            {!!sessionId && (
              <div className="px-2.5 pb-1 text-[11px] text-muted-foreground">
                {topent
                  ? '会话覆盖值(已下发到当前会话)'
                  : topentShell
                    ? (topentCfg
                      ? (topentCfg === topentShell
                        ? '当前会话值(连接时已按环境配置下发)'
                        : `当前会话值;环境配置已改为 ${topentCfg},下一轮调试生效`)
                      : '当前会话值(环境未配置,沿用选区登录默认)')
                    : (topentCfg ? '未取到实际值,显示的是环境配置值' : '未配置 TOPENT(可手动填写后保存)')}
              </div>
            )}
            {topentErr && <div className="px-2.5 pb-1 text-[11px] text-red-600 dark:text-red-400">{topentErr}</div>}
          </div>
        )}
      </div>

      {/* 会话操作二次确认(shadcn AlertDialog;确认后执行切换/重启/结束) */}
      <AlertDialog open={pending !== null} onOpenChange={(o) => { if (!o) setPending(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{pending?.title || ''}</AlertDialogTitle>
            <AlertDialogDescription>{pending?.desc || ''}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={confirmPending}>确定</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
