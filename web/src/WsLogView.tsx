// 接口日志视图:Chrome DevTools Network 风格。
// 默认只有日志列表(全宽);点击某一行才在右侧"嵌入"详情面板(基本信息/Request/Response + 重放调试),
// 此时列表收起为「状态 + 服务」两列给面板让位 —— 详情面板可拖拽分配宽度,也可主动关闭(✕ / Esc)。
// 查询条件对齐 awsq990 主查询 QBE:服务名(wsfa001)+ 开始时间范围(wsfa003);仅失败为本工具扩展
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Bug, ChevronLeft, ChevronRight, RefreshCw, X } from 'lucide-react'
import { useStore } from './store'
import { Checkbox, DatePicker } from './ui'
import type { WSLogItem } from './api'

// 列表列定义:完整模式(未选中)与紧凑模式(详情展开,只留状态/服务)共用同一套渲染
type Col = { key: string; head: string; cls: string; render: (it: WSLogItem) => ReactNode }

const COL_STATUS: Col = {
  key: 'status', head: '状态', cls: 'w-12 shrink-0',
  render: (it) => (
    <span className={`font-mono text-[11px] ${it.code === '000' ? 'text-emerald-600 dark:text-emerald-500' : it.code ? 'text-red-500' : 'text-muted-foreground'}`}>
      {it.code || '-'}
    </span>
  ),
}
const COL_SERVICE: Col = {
  key: 'service', head: '服务', cls: 'w-44 shrink-0',
  render: (it) => <span className="truncate text-foreground" title={it.service}>{it.service}</span>,
}
const COL_JOB: Col = {
  key: 'job', head: '作业', cls: 'w-28 shrink-0',
  render: (it) => <span className="truncate text-sky-600 dark:text-sky-400" title={it.job}>{it.job}</span>,
}
const COL_START: Col = {
  key: 'start', head: '开始时间', cls: 'w-32 shrink-0',
  render: (it) => <span className="font-mono text-[11px] text-muted-foreground">{it.start}</span>,
}
const COL_DUR: Col = {
  key: 'dur', head: '耗时(s)', cls: 'w-16 shrink-0',
  render: (it) => <span className="font-mono text-[11px] text-muted-foreground">{it.duration}</span>,
}
const COL_ERR: Col = {
  key: 'err', head: '错误描述', cls: 'min-w-0 flex-1',
  render: (it) => <span className="truncate text-red-600 dark:text-red-400/90" title={it.errMsg}>{it.errMsg}</span>,
}
const COLS_FULL: Col[] = [COL_STATUS, COL_SERVICE, COL_JOB, COL_START, COL_DUR, COL_ERR]
// 详情展开时:只留「状态 + 服务 + 作业 + 开始时间」(耗时/错误描述在右侧详情「基本信息」里都有)
const COLS_COMPACT: Col[] = [COL_STATUS, { ...COL_SERVICE, cls: 'min-w-0 flex-1' }, COL_JOB, COL_START]

// 左右分栏按「比例」分配(默认五五分):拖拽只改比例,容器再窄也有可拖区间;
// 窗口缩放时两栏等比跟随,不会像固定像素那样一边被夹死、拖不动
const RATIO_MIN = 0.25 // 详情面板最小占比
const RATIO_MAX = 0.75 // 详情面板最大占比
const RESIZER_W = 5    // .col-resizer 固定占位

export function WsLogView() {
  const wsLogs = useStore((s) => s.wsLogs)
  const loading = useStore((s) => s.wsLogsLoading)
  const page = useStore((s) => s.wsLogsPage)
  const hasMore = useStore((s) => s.wsLogsHasMore)
  const sel = useStore((s) => s.wsLogSel)
  const content = useStore((s) => s.wsLogContent)
  const tab = useStore((s) => s.wsLogTab)
  const err = useStore((s) => s.wsLogErr)
  const view = useStore((s) => s.view)
  const loadWsLogs = useStore((s) => s.loadWsLogs)
  const selectWsLog = useStore((s) => s.selectWsLog)
  const closeWsLogDetail = useStore((s) => s.closeWsLogDetail)
  const setWsLogTab = useStore((s) => s.setWsLogTab)
  const replayDebug = useStore((s) => s.replayDebug)
  const sessionId = useStore((s) => s.sessionId)
  const state = useStore((s) => s.state)
  // 宿主会话常驻:结束调试后会话仍空闲保留,不算"进行中";只有真在跑/停着/启动
  // 才算忙碌。重放按钮不设禁用——后端重放会先自动收口现有会话(见 wslog 重放接口)
  const sessionBusy = !!sessionId && state !== 'idle' && state !== 'exit' && state !== ''
  const [service, setService] = useState('')
  const [onlyFail, setOnlyFail] = useState(false)
  // 默认过滤条件:当天(awsq990 查当天日志是最常用场景)
  const today = () => {
    const d = new Date()
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  }
  const [from, setFrom] = useState(today)
  const [to, setTo] = useState(today)

  // 接口日志按"当前会话所属环境"的数据库查询:没有会话就没有可查的数据源,整页不显示内容
  const hasSession = !!sessionId && state !== 'exit'
  useEffect(() => {
    if (!hasSession) return
    void loadWsLogs('', false, 1, from, to)
  }, [loadWsLogs, hasSession, from, to])

  const doLoad = (p = 1) => void loadWsLogs(service, onlyFail, p, from, to)

  // ---- 宽度分配:左右两栏按比例分(默认 1:1),之和恒等于容器宽度 ----
  // 容器宽度实测(窗口/侧边栏变化时跟随);拖拽改的是占比,容器再窄也不会夹到"不可拖"的死区
  const splitRef = useRef<HTMLDivElement>(null)
  const [boxW, setBoxW] = useState(0)
  useEffect(() => {
    const el = splitRef.current
    if (!el) return
    const ro = new ResizeObserver(() => setBoxW(el.clientWidth))
    ro.observe(el)
    setBoxW(el.clientWidth)
    return () => ro.disconnect()
  }, [])
  const [ratio, setRatio] = useState(() => {
    const v = Number(localStorage.getItem('tdebug.wslogRatio'))
    return v >= RATIO_MIN && v <= RATIO_MAX ? v : 0.5
  })
  const splittable = Math.max(1, boxW - RESIZER_W)
  const detailW = Math.round(splittable * ratio)

  const onDetailResizeDown = (e: React.MouseEvent<HTMLDivElement>) => {
    e.preventDefault()
    const el = e.currentTarget
    el.classList.add('dragging')
    const startX = e.clientX
    const startRatio = ratio
    let latest = ratio
    const move = (ev: MouseEvent) => {
      // 分隔线跟手:向左拖 = 详情面板变宽(与 VS Code 侧边栏一致);只改占比,总量不变
      const box = splitRef.current?.clientWidth || 0
      const sp = Math.max(1, box - RESIZER_W)
      latest = Math.min(RATIO_MAX, Math.max(RATIO_MIN, startRatio - (ev.clientX - startX) / sp))
      setRatio(latest)
    }
    const up = () => {
      el.classList.remove('dragging')
      localStorage.setItem('tdebug.wslogRatio', String(latest))
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  // Esc 关闭详情(仅在本视图可见时生效,避免影响编辑器/调试页)
  useEffect(() => {
    if (!sel || view !== 'wslogs') return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') { e.preventDefault(); closeWsLogDetail() }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [sel, view, closeWsLogDetail])

  const cols = sel ? COLS_COMPACT : COLS_FULL

  // 未连接会话:没有可查的数据源,只给一句引导,不渲染工具条/列表/详情
  if (!hasSession) {
    return (
      <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-1.5 p-6 text-center text-xs text-muted-foreground">
        <span className="text-sm text-foreground">未连接会话</span>
        <span>接口日志按当前会话所属环境的数据库查询。</span>
        <span>请先在「会话」面板选择一个环境连接,再回到这里查看。</span>
      </div>
    )
  }

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      {/* 工具条:服务名 + 时间范围(awsq990 QBE 同款条件)+ 仅失败 + 刷新 + 翻页;下缘分割线与列表连成整体 */}
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-2 py-1.5">
        <input
          value={service}
          onChange={(e) => setService(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') doLoad(1) }}
          placeholder="按服务名过滤,如 icd.erp.wo*(回车生效)"
          className="h-7 w-56 border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground focus:border-border focus:outline-none"
        />
        <div className="flex items-center gap-1 text-xs text-muted-foreground">
          开始时间
          <DatePicker value={from} onChange={setFrom} title="查询开始日期" />
          ~
          <DatePicker value={to} onChange={setTo} title="查询结束日期" />
        </div>
        <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <Checkbox id="wslog-onlyfail" checked={onlyFail}
            onCheckedChange={(v) => { setOnlyFail(v === true); setTimeout(() => doLoad(1), 0) }} />
          <label htmlFor="wslog-onlyfail" className="cursor-pointer select-none">仅失败</label>
        </div>
        <button onClick={() => doLoad(1)} disabled={loading} title="重新加载"
          className="p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-40">
          <RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
        </button>
        {/* 分页 */}
        <div className="ml-auto flex items-center gap-1 text-xs text-muted-foreground">
          <span>{wsLogs.length} 条</span>
          <button onClick={() => doLoad(page - 1)} disabled={loading || page <= 1} title="上一页"
            className="p-1 hover:bg-accent hover:text-foreground disabled:opacity-30">
            <ChevronLeft className="h-4 w-4" />
          </button>
          <span className="whitespace-nowrap font-mono">第 {page} 页</span>
          <button onClick={() => doLoad(page + 1)} disabled={loading || !hasMore} title="下一页"
            className="p-1 hover:bg-accent hover:text-foreground disabled:opacity-30">
            <ChevronRight className="h-4 w-4" />
          </button>
        </div>
        {sessionBusy && <span className="text-xs text-amber-600/80 dark:text-amber-500/80">调试会话忙碌中:重放将自动结束当前调试</span>}
      </div>

      {err && (
        <div className="mb-2 shrink-0 border border-red-500/20 bg-red-500/10 px-3 py-1.5 text-xs text-red-600 dark:text-red-600 dark:text-red-400">{err}</div>
      )}

      {/* 左右分配:左侧列表 flex-1 吃掉剩余宽度,右侧详情按占比取宽 + 5px 分隔条。
          min-w-0/overflow-hidden 锁住容器宽度:面板是 shrink-0,不加锁会把容器一路撑宽
          (宽度反馈环 → 整页横向溢出) */}
      <div ref={splitRef} className="flex min-h-0 min-w-0 flex-1 overflow-hidden">
        {/* 列表:表头与行同处一个滚动容器,横向滚动时表头跟着滚不错位 */}
        <div className="flex min-w-0 flex-1 flex-col overflow-hidden bg-background">
          <div className="min-h-0 flex-1 overflow-auto">
            <div className="sticky top-0 flex h-7 items-center gap-2 border-b border-border bg-background px-2 text-[11px] font-medium text-muted-foreground">
              {cols.map((c) => <span key={c.key} className={c.cls}>{c.head}</span>)}
            </div>
            {wsLogs.length === 0 && !loading && (
              <div className="p-4 text-center text-xs text-muted-foreground">暂无日志记录</div>
            )}
            {wsLogs.map((it, i) => (
              <div key={it.rowid} onClick={() => void selectWsLog(it)}
                title={sel ? undefined : '点击在右侧查看请求/响应报文'}
                className={`flex h-7 cursor-pointer items-center gap-2 px-2 text-xs ${
                  sel?.rowid === it.rowid ? 'bg-sky-500/10' : i % 2 ? 'bg-card/40 hover:bg-accent/40' : 'hover:bg-accent/40'
                }`}>
                {cols.map((c) => <span key={c.key} className={`${c.cls} flex items-center`}>{c.render(it)}</span>)}
              </div>
            ))}
          </div>
        </div>

        {/* 详情(右列,仅选中行后出现):贴右缘整高嵌入,左侧 1px 分割线即拖拽把手 */}
        {sel && (
          <>
            <div className="col-resizer self-stretch" onMouseDown={onDetailResizeDown} title="拖拽分配宽度" />
            <div style={{ width: boxW ? detailW : '50%' }} className="flex shrink-0 flex-col overflow-hidden bg-card">
              <div className="flex h-8 shrink-0 items-center gap-1 border-b border-border px-2">
                <button onClick={closeWsLogDetail} title="关闭详情(Esc)"
                  className="p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground">
                  <X className="h-3.5 w-3.5" />
                </button>
                {([['info', '基本信息'], ['request', 'Request'], ['response', 'Response']] as const).map(([k, label]) => (
                  <button key={k} onClick={() => setWsLogTab(k)}
                    className={`px-2 py-0.5 text-xs ${tab === k ? 'bg-accent text-foreground' : 'text-muted-foreground hover:text-foreground'}`}>
                    {label}
                  </button>
                ))}
                <button
                  onClick={() => void replayDebug(sel)}
                  title="用该日志的报文重放此接口调用并进入调试(T100 r.dg 同款;现有会话会自动收口)"
                  className="ml-auto inline-flex items-center gap-1 border border-emerald-500/20 px-2 py-0.5 text-xs text-emerald-600 dark:text-emerald-400 hover:bg-emerald-500/10"
                >
                  <Bug className="h-3.5 w-3.5" />
                  调试此调用
                </button>
              </div>
              <div className="min-h-0 flex-1 overflow-auto">
                {tab === 'info' && <InfoBody item={sel} />}
                {tab === 'request' && <PayloadBody text={content?.request} loading={!content} />}
                {tab === 'response' && <PayloadBody text={content?.response} loading={!content} />}
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// 基本信息(DevTools Headers 风格的两列键值)
function InfoBody({ item }: { item: WSLogItem }) {
  const rows: [string, string][] = [
    ['服务', item.service],
    ['作业', item.job],
    ['返回码', item.code],
    ['开始时间', item.start],
    ['耗时(s)', item.duration],
    ['进程 PID', item.pid],
    ['错误描述', item.errMsg],
    ['请求报文文件', item.reqPath],
    ['响应报文文件', item.rspPath],
    ['请求大小(字节)', item.reqSize],
    ['响应大小(字节)', item.rspSize],
  ]
  return (
    <div className="p-2 text-xs">
      {rows.map(([k, v]) => (
        <div key={k} className="flex gap-2 border-b border-border/60 py-1">
          <span className="w-28 shrink-0 text-muted-foreground">{k}</span>
          <span className="min-w-0 flex-1 break-all text-foreground">{v || '-'}</span>
        </div>
      ))}
    </div>
  )
}

// 报文内容(monospace pre)
function PayloadBody({ text, loading }: { text?: string; loading: boolean }) {
  if (loading) return <div className="p-3 text-xs text-muted-foreground">加载报文中…</div>
  if (!text) return <div className="p-3 text-xs text-muted-foreground">无报文(超过入库大小上限且源文件已清理)</div>
  return (
    <pre className="whitespace-pre-wrap break-all p-2 font-mono text-[11px] leading-5 text-foreground">{text}</pre>
  )
}
