// 接口日志视图:Chrome DevTools Network 风格。
// 默认只有日志列表(全宽);点击某一行才在右侧"嵌入"详情面板(基本信息/Request/Response + 重放调试),
// 此时列表收起为「状态 + 服务」两列给面板让位 —— 详情面板可拖拽分配宽度,也可主动关闭(✕ / Esc)。
// 查询条件对齐 awsq990 主查询 QBE:服务名(wsfa001)+ 开始时间范围(wsfa003);仅失败为本工具扩展
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Bug, Check, ChevronDown, ChevronLeft, ChevronRight, ChevronsUpDown, ChevronUp, Copy, Fingerprint, RefreshCw, Undo2, X } from 'lucide-react'
import { useStore } from './store'
import { Checkbox, DatePicker, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui'
import type { WSLogItem } from './api'
import { PayloadEditor } from './PayloadEditor'

// 列表列定义:完整模式(未选中)与紧凑模式(详情展开,只留状态/服务)共用同一套渲染。
// 每个列都给出排序键,表头点击即排序。注意:排序只作用于**当前页**——分页在后端做,
// 未排序时的顺序就是服务端的 `order by wsfa003 desc`(最新在前)。
// 列集对齐 T100 原生页 awsq990.4gl(整合服務端檢測工具,见 debug/wslog.go 注释):
// 它显示 起始时间/发起端/服务端/服务名称/服务程序序号/状态/处理时间/错误消息/sso秒数 九列,
// 本工具此前只展示其中一部分,现补齐「服务程序序号 / 发起端 / 服务端 / sso时间」四项。
type SortKey = 'status' | 'service' | 'job' | 'pid' | 'start' | 'end' | 'dur' | 'sso' | 'origin' | 'server' | 'err'
type Col = {
  key: SortKey
  head: string
  cls: string // table-fixed 下的列宽;留空 = 吃掉剩余宽度(每套列里只应有一个)
  render: (it: WSLogItem) => ReactNode
  sortValue: (it: WSLogItem) => string | number
}

// 数值列排序:wsfa005/wsfa015 在库里是文本/数值混着取回的字符串,按数值比才对
const numSort = (s: string): number => {
  const n = Number.parseFloat(s)
  return Number.isFinite(n) ? n : Number.NaN
}

const COL_STATUS: Col = {
  key: 'status', head: '状态', cls: 'w-14',
  sortValue: (it) => it.code,
  render: (it) => (
    <span className={`font-mono text-[11px] ${it.code === '000' ? 'text-emerald-600 dark:text-emerald-500' : it.code ? 'text-red-500' : 'text-muted-foreground'}`}>
      {it.code || '-'}
    </span>
  ),
}
const COL_SERVICE: Col = {
  key: 'service', head: '服务', cls: 'w-40',
  sortValue: (it) => it.service,
  render: (it) => <span className="block truncate text-foreground" title={it.service}>{it.service}</span>,
}
const COL_JOB: Col = {
  key: 'job', head: '作业', cls: 'w-28',
  sortValue: (it) => it.job,
  render: (it) => <span className="block truncate text-sky-600 dark:text-sky-400" title={it.job}>{it.job}</span>,
}
const COL_PID: Col = {
  key: 'pid', head: '服务程序序号', cls: 'w-24',
  sortValue: (it) => numSort(it.pid),
  render: (it) => <span className="block truncate font-mono text-[11px] text-muted-foreground" title={it.pid}>{it.pid}</span>,
}
const COL_START: Col = {
  key: 'start', head: '开始时间', cls: 'w-36',
  // wsfa003 取出即 "yyyy-mm-dd hh:mm:ss",字典序 == 时间序(后端 SQL 也是这么比的)
  sortValue: (it) => it.start,
  render: (it) => <span className="font-mono text-[11px] text-muted-foreground">{it.start}</span>,
}
const COL_END: Col = {
  key: 'end', head: '结束时间', cls: 'w-36',
  // wsfa004(原生页取而不显的一列);列表里截到秒,毫秒位只参与后端时间窗过滤
  sortValue: (it) => it.end,
  render: (it) => <span className="font-mono text-[11px] text-muted-foreground">{it.end}</span>,
}
const COL_DUR: Col = {
  key: 'dur', head: '耗时(s)', cls: 'w-20',
  // wsfa005 是文本列,按数值排(空/非法值恒排最后)
  sortValue: (it) => numSort(it.duration),
  render: (it) => <span className="font-mono text-[11px] text-muted-foreground">{it.duration}</span>,
}
const COL_SSO: Col = {
  key: 'sso', head: 'sso时间(秒)', cls: 'w-24',
  sortValue: (it) => numSort(it.sso),
  render: (it) => <span className="block truncate font-mono text-[11px] text-muted-foreground" title={it.sso}>{it.sso}</span>,
}
const COL_ORIGIN: Col = {
  key: 'origin', head: '发起端', cls: 'w-24',
  sortValue: (it) => it.origin,
  render: (it) => <span className="block truncate text-muted-foreground" title={it.origin}>{it.origin}</span>,
}
const COL_SERVER: Col = {
  key: 'server', head: '服务端', cls: 'w-24',
  sortValue: (it) => it.server,
  render: (it) => <span className="block truncate text-muted-foreground" title={it.server}>{it.server}</span>,
}
const COL_ERR: Col = {
  key: 'err', head: '错误描述', cls: '',
  sortValue: (it) => it.errMsg,
  render: (it) => <span className="block truncate text-red-600 dark:text-red-400/90" title={it.errMsg}>{it.errMsg}</span>,
}
const COLS_FULL: Col[] = [
  COL_STATUS, COL_SERVICE, COL_JOB, COL_PID, COL_START, COL_END, COL_DUR, COL_SSO, COL_ORIGIN, COL_SERVER, COL_ERR,
]
// 详情展开时:只留「状态 + 服务 + 作业 + 开始时间」(其余列在右侧详情「基本信息」里都有);
// 服务列去掉固定宽度,让它吃掉剩余空间
const COLS_COMPACT: Col[] = [COL_STATUS, { ...COL_SERVICE, cls: '' }, COL_JOB, COL_START]

// 左右分栏按「比例」分配(默认五五分):拖拽只改比例,容器再窄也有可拖区间;
// 窗口缩放时两栏等比跟随,不会像固定像素那样一边被夹死、拖不动
const RATIO_MIN = 0.25 // 详情面板最小占比
const RATIO_MAX = 0.75 // 详情面板最大占比
const RESIZER_W = 5    // .col-resizer 固定占位

// 工具条查询输入框(四个条件共用外观;服务名称放通配,给宽一点)
const QUERY_INPUT = 'h-7 w-52 border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground focus:border-border focus:outline-none'
const QUERY_INPUT_SM = 'h-7 w-36 border border-border bg-background px-2 text-xs text-foreground placeholder:text-muted-foreground focus:border-border focus:outline-none'

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
  const prefetchWsLog = useStore((s) => s.prefetchWsLog)
  const closeWsLogDetail = useStore((s) => s.closeWsLogDetail)
  const setWsLogTab = useStore((s) => s.setWsLogTab)
  const replayDebug = useStore((s) => s.replayDebug)
  const sessionId = useStore((s) => s.sessionId)
  const state = useStore((s) => s.state)
  // 宿主会话常驻:结束调试后会话仍空闲保留,不算"进行中";只有真在跑/停着/启动
  // 才算忙碌。重放按钮不设禁用——后端重放会先自动收口现有会话(见 wslog 重放接口)
  const sessionBusy = !!sessionId && state !== 'idle' && state !== 'exit' && state !== ''
  // 顶部查询条件:四个输入框存 draft(正在编辑),回车/刷新才写入 applied 并查询;
  // 日期/仅失败仍即时生效(沿用原有交互)。
  // 条件语义对齐 T100 原生页 awsq990:服务名称 wsfa001(支持 * ? 通配)、
  // 处理结果 wsfa006、发起端 wsfa013、服务端 wsfa018(后三者等值)。
  const [draft, setDraft] = useState({ service: '', job: '', result: '', origin: '', server: '' })
  const [applied, setApplied] = useState(draft)
  const [onlyFail, setOnlyFail] = useState(false)
  // 日期变化时要复用"最新已提交条件",用 ref 读避免把它写进 effect 依赖
  // (写进去会因输入框每次按键都触发查询);此前此处传空串,改日期会把已填条件清掉。
  const appliedRef = useRef(applied)
  appliedRef.current = applied
  const onlyFailRef = useRef(onlyFail)
  onlyFailRef.current = onlyFail
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
    void loadWsLogs({ ...appliedRef.current, onlyFail: onlyFailRef.current, page: 1, startFrom: from, endTo: to })
  }, [loadWsLogs, hasSession, from, to])

  // 提交查询:Enter / 点刷新 / 翻页都走这里(翻页沿用已提交条件,不读 draft)
  const doLoad = (p = 1) => {
    setApplied(draft)
    void loadWsLogs({ ...draft, onlyFail, page: p, startFrom: from, endTo: to })
  }
  const onEnter = (e: React.KeyboardEvent) => { if (e.key === 'Enter') doLoad(1) }

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

  // ---- 排序:点表头循环 升序 → 降序 → 恢复默认(服务端的 wsfa003 desc,最新在前) ----
  const [sort, setSort] = useState<{ key: SortKey; dir: 'asc' | 'desc' } | null>(null)
  const toggleSort = (key: SortKey) => setSort((s) => {
    if (s?.key !== key) return { key, dir: 'asc' }
    return s.dir === 'asc' ? { key, dir: 'desc' } : null
  })
  const rows = useMemo(() => {
    if (!sort) return wsLogs
    // 排序键始终从完整列集里取:详情展开后列表切成紧凑列(无耗时/错误描述),
    // 此时排序仍然生效,只是没有那一列的表头指示箭头
    const col = COLS_FULL.find((c) => c.key === sort.key)
    if (!col) return wsLogs
    const sign = sort.dir === 'asc' ? 1 : -1
    // 空值/非法值恒排最后(不随升降序翻面),否则空串会把整列有效值挤到下面
    const emptyLast = (a: string, b: string) => (!a && !b ? 0 : !a ? 1 : !b ? -1 : null)
    return [...wsLogs].sort((x, y) => {
      const va = col.sortValue(x)
      const vb = col.sortValue(y)
      if (typeof va === 'number' && typeof vb === 'number') {
        const na = Number.isNaN(va)
        const nb = Number.isNaN(vb)
        if (na || nb) return na && nb ? 0 : na ? 1 : -1
        return (va - vb) * sign
      }
      const e = emptyLast(String(va), String(vb))
      if (e !== null) return e
      return String(va).localeCompare(String(vb)) * sign
    })
  }, [wsLogs, sort])

  // ---- 报文复制(Request / Response 两个页面的右上角按钮) ----
  const payloadText = tab === 'request' ? content?.request : tab === 'response' ? content?.response : ''
  const [copyState, setCopyState] = useState<'ok' | 'fail' | null>(null)
  // 换页签/换记录后不残留上一次的"已复制"反馈
  useEffect(() => { setCopyState(null) }, [tab, sel?.rowid])
  const copyPayload = async () => {
    if (!payloadText) return
    try {
      await navigator.clipboard.writeText(payloadText)
      setCopyState('ok')
    } catch {
      setCopyState('fail')
    }
    window.setTimeout(() => setCopyState(null), 1500)
  }

  // ---- 唯一标识复制 ----
  // 人看到可疑的一行 → 点一下把它的唯一标识(rowid / ctid)复制走 → 粘到对话里
  // → AI 用 tdebug wsdebug <标识> 直接重放这一行。复制内容**就是标识本身**,
  // 不带任何包装文案,免得 AI 还要从周围文字里把它抠出来。
  const [idState, setIdState] = useState<'ok' | 'fail' | null>(null)
  useEffect(() => { setIdState(null) }, [sel?.rowid])
  const copyRowID = async () => {
    if (!sel?.rowid) return
    try {
      await navigator.clipboard.writeText(sel.rowid)
      setIdState('ok')
    } catch {
      setIdState('fail')
    }
    window.setTimeout(() => setIdState(null), 1500)
  }

  // 悬停预取:在行上停留 ~120ms 就开始取该行报文,点下去时通常已就绪(划过不停留则不触发)。
  // 只对当前页可见的列做,鼠标快速扫过不会发请求。
  const prefetchTimer = useRef<number | undefined>(undefined)
  const onRowEnter = (it: WSLogItem) => {
    window.clearTimeout(prefetchTimer.current)
    prefetchTimer.current = window.setTimeout(() => prefetchWsLog(it), 120)
  }
  const onRowLeave = () => window.clearTimeout(prefetchTimer.current)

  // ---- 入参编辑(重放用) ----
  // reqDraft = 编辑器里的当前文本(草稿);与日志里的原始入参不一致即视为"改过",
  // 重放时把草稿发给后端(后端落临时文件后作为入参文件),未改则走原报文。
  // 换行/换报文时重置草稿,避免把上一行的修改带到下一行。
  const [reqDraft, setReqDraft] = useState('')
  useEffect(() => { setReqDraft(content?.request ?? '') }, [content?.request, sel?.rowid])
  const reqDirty = !!content && reqDraft !== content.request

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
      {/* 工具条分两行(条件多,挤一行看不清):
            第一行 = 四个查询条件(服务名称/服务端/发起端/处理结果,对齐 awsq990 QBE)+ 刷新;
            第二行 = 时间范围 + 仅失败 + 条数/翻页(右对齐)。
          下缘分割线与列表连成整体;窄了各自内部换行(flex-wrap)。
          两行之间留出间距(pt/pb),否则输入框与日期行贴在一起显得挤。 */}
      <div className="shrink-0 border-b border-border">
        <div className="flex flex-wrap items-center gap-2 px-2 pt-2 pb-1.5">
          <input
            value={draft.service}
            onChange={(e) => setDraft((d) => ({ ...d, service: e.target.value }))}
            onKeyDown={onEnter}
            placeholder="服务名称(支持 * ? 通配,回车生效)"
            title="服务名称 wsfa001:支持 * ? 通配,如 icd.erp.wo*;回车生效"
            className={QUERY_INPUT}
          />
          <input
            value={draft.job}
            onChange={(e) => setDraft((d) => ({ ...d, job: e.target.value }))}
            onKeyDown={onEnter}
            placeholder="作业编号(支持 * ? 通配)"
            title="作业编号 wsfa012:支持 * ? 通配,如 wssp01131 / wssp*。服务名是 oa.schema.data.get 这类反域名,认不出是哪个作业,按作业找要用这个;回车生效"
            className={QUERY_INPUT_SM}
          />
          <input
            value={draft.server}
            onChange={(e) => setDraft((d) => ({ ...d, server: e.target.value }))}
            onKeyDown={onEnter}
            placeholder="服务端(如 T100)"
            title="服务端 wsfa018:等值匹配,如 T100;回车生效"
            className={QUERY_INPUT_SM}
          />
          <input
            value={draft.origin}
            onChange={(e) => setDraft((d) => ({ ...d, origin: e.target.value }))}
            onKeyDown={onEnter}
            placeholder="发起端(如 OA)"
            title="发起端 wsfa013:等值匹配,如 OA / Athena;回车生效"
            className={QUERY_INPUT_SM}
          />
          <input
            value={draft.result}
            onChange={(e) => setDraft((d) => ({ ...d, result: e.target.value }))}
            onKeyDown={onEnter}
            placeholder="处理结果(如 000)"
            title="处理结果 wsfa006:等值匹配,如 000 成功 / 100 失败;回车生效"
            className={QUERY_INPUT_SM}
          />
          <button onClick={() => doLoad(1)} disabled={loading} title="按当前条件重新加载(回车等效)"
            className="p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-40">
            <RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
          </button>
        </div>
        <div className="flex flex-wrap items-center gap-2 px-2 pt-1.5 pb-2">
          <div className="flex items-center gap-1 text-xs text-muted-foreground">
            起始时间
            <DatePicker value={from} onChange={setFrom} title="起始时间 wsfa003 的下界(含当天)" />
            ~
            结束时间
            <DatePicker value={to} onChange={setTo} title="结束时间 wsfa004 的上界(含当天,补到 23:59:59.99999)" />
          </div>
          <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Checkbox id="wslog-onlyfail" checked={onlyFail}
              onCheckedChange={(v) => {
                // 立即生效:显式传新值查询。原先靠 setTimeout(doLoad) 复用闭包里的旧 onlyFail,
                // 首次勾选实际发的是旧值(勾上了但结果没变),这里一并修掉。
                const on = v === true
                setOnlyFail(on)
                setApplied(draft)
                void loadWsLogs({ ...draft, onlyFail: on, page: 1, startFrom: from, endTo: to })
              }} />
            <label htmlFor="wslog-onlyfail" className="cursor-pointer select-none">仅失败</label>
          </div>
          {sessionBusy && <span className="text-xs text-amber-600/80 dark:text-amber-500/80">调试会话忙碌中:重放将自动结束当前调试</span>}
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
        </div>
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
            {/* table-fixed:列宽交给表头/单元格上的 w-* ,留空的那列吃剩余宽度。
                container=false 不套 shadcn Table 默认的 overflow-x-auto 容器(它会让 th 的
                sticky 失效),滚动交给外层。
                网格线用 shadcn Table 的单元格边框(设置页那张表的全框线观感),但必须:
                  · border-separate + border-spacing-0 —— border-collapse 下边框由表格整体绘制,
                    sticky 的 th 带背景压在上面时整行框线会看不见;
                  · 单元格只留 右+下(border-t-0/border-l-0),否则相邻格子边框不合并会叠成 2px;
                  · 表头背景必须不透明且与列表同色(bg-background),否则滚动时行会从表头透出来。 */}
            <Table container={false} className="table-fixed border-separate border-spacing-0 text-xs">
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  {cols.map((c) => {
                    const active = sort?.key === c.key
                    const tip = active
                      ? (sort!.dir === 'asc' ? '升序,点击改降序' : '降序,点击恢复默认(最新在前)')
                      : '点击按此列升序排序'
                    return (
                      <TableHead key={c.key} className={`sticky top-0 z-10 border-t-0 border-l-0 bg-background ${c.cls}`}>
                        <button onClick={() => toggleSort(c.key)} title={tip}
                          className="group inline-flex h-7 w-full items-center gap-1 text-left text-[11px] font-medium text-muted-foreground transition-colors hover:text-foreground">
                          <span className="min-w-0 truncate">{c.head}</span>
                          {active
                            ? (sort!.dir === 'asc'
                              ? <ChevronUp className="h-3 w-3 shrink-0 text-foreground" />
                              : <ChevronDown className="h-3 w-3 shrink-0 text-foreground" />)
                            : <ChevronsUpDown className="h-3 w-3 shrink-0 opacity-0 transition-opacity group-hover:opacity-60" />}
                        </button>
                      </TableHead>
                    )
                  })}
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((it) => {
                  const selected = sel?.rowid === it.rowid
                  return (
                    <TableRow key={it.rowid} data-state={selected ? 'selected' : undefined}
                      onClick={() => void selectWsLog(it)}
                      onMouseEnter={() => onRowEnter(it)}
                      onMouseLeave={onRowLeave}
                      title={sel ? undefined : '点击在右侧查看请求/响应报文'}
                      className="h-7 cursor-pointer hover:bg-accent/40 data-[state=selected]:bg-sky-500/10">
                      {cols.map((c) => (
                        <TableCell key={c.key} className={`border-t-0 border-l-0 px-2 py-0 ${c.cls}`}>{c.render(it)}</TableCell>
                      ))}
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
            {rows.length === 0 && !loading && (
              <div className="p-4 text-center text-xs text-muted-foreground">暂无日志记录</div>
            )}
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
                    className={`inline-flex items-center gap-1 px-2 py-0.5 text-xs ${tab === k ? 'bg-accent text-foreground' : 'text-muted-foreground hover:text-foreground'}`}>
                    {label}
                    {k === 'request' && reqDirty && <span className="text-amber-600 dark:text-amber-400" title="入参已修改,重放会用它">•</span>}
                  </button>
                ))}
                {reqDirty && (
                  <button onClick={() => setReqDraft(content?.request ?? '')}
                    title="放弃修改,还原为日志里的原始入参"
                    className="inline-flex items-center gap-1 border border-border px-2 py-0.5 text-xs text-muted-foreground hover:bg-accent hover:text-foreground">
                    <Undo2 className="h-3.5 w-3.5" />
                    还原入参
                  </button>
                )}
                <button
                  onClick={() => void replayDebug(sel, reqDirty ? reqDraft : undefined)}
                  disabled={reqDirty && !!content?.requestPartial}
                  title={reqDirty && content?.requestPartial
                    ? '入参内容不完整(被截断或只剩入库前 2000 字符),不能基于它重放'
                    : reqDirty
                      ? '用上面编辑器里改过的入参重放此接口调用'
                      : '用该日志的原始报文重放此接口调用(T100 r.dg 同款;现有会话会自动收口)'}
                  className="ml-auto inline-flex items-center gap-1 border border-emerald-500/20 px-2 py-0.5 text-xs text-emerald-600 dark:text-emerald-400 hover:bg-emerald-500/10 disabled:pointer-events-none disabled:opacity-40"
                >
                  <Bug className="h-3.5 w-3.5" />
                  {reqDirty ? '用修改后的入参重放' : '调试此调用'}
                </button>
                {/* 把这行日志的唯一标识交给 AI:AI 用 tdebug wsdebug <标识> 直接重放这一行 */}
                <button
                  onClick={() => void copyRowID()}
                  title="复制这行日志的唯一标识(rowid / ctid)。把它发给 AI,AI 就能用 tdebug wsdebug <标识> 直接重放这一行"
                  className={`inline-flex shrink-0 items-center gap-1 border px-2 py-0.5 text-xs transition-colors ${
                    idState === 'fail'
                      ? 'border-red-500/20 text-red-600 dark:text-red-400'
                      : 'border-border text-muted-foreground hover:bg-accent hover:text-foreground'
                  }`}
                >
                  {idState === 'ok' ? <Check className="h-3.5 w-3.5" /> : <Fingerprint className="h-3.5 w-3.5" />}
                  {idState === 'ok' ? '已复制' : idState === 'fail' ? '复制失败' : '复制标识'}
                </button>
                {/* 报文页(Request / Response)的复制按钮:放在最右,即详情面板右上角 */}
                {tab !== 'info' && (
                  <button
                    onClick={() => void copyPayload()}
                    disabled={!payloadText}
                    title={payloadText ? '复制报文到剪贴板' : '报文未加载或无内容'}
                    className={`inline-flex shrink-0 items-center gap-1 border px-2 py-0.5 text-xs transition-colors disabled:pointer-events-none disabled:opacity-40 ${
                      copyState === 'fail'
                        ? 'border-red-500/20 text-red-600 dark:text-red-400'
                        : 'border-border text-muted-foreground hover:bg-accent hover:text-foreground'
                    }`}
                  >
                    {copyState === 'ok' ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                    {copyState === 'ok' ? '已复制' : copyState === 'fail' ? '复制失败' : '复制'}
                  </button>
                )}
              </div>
              {/* 内容不完整时点明:按原报文重放没问题,但不能在编辑器里改完再重放 */}
              {tab === 'request' && content?.requestPartial && (
                <div className="shrink-0 border-b border-border bg-amber-500/10 px-2.5 py-1 text-[11px] text-amber-600 dark:text-amber-400">
                  入参内容不完整(超过读取上限被截断,或源文件已清理只剩入库前 2000 字符):可以按原报文重放,但不能用修改后的入参重放。
                </div>
              )}
              {/* 基本信息是普通内容流,需要外层滚动;报文页交给 Monaco 自己滚(嵌套滚动容器会出多余滚动条) */}
              <div className={`min-h-0 flex-1 ${tab === 'info' ? 'overflow-auto' : 'overflow-hidden'}`}>
                {tab === 'info' && <InfoBody item={sel} />}
                {tab === 'request' && (
                  <PayloadBody text={reqDraft} loading={!content} editable onChange={setReqDraft}
                    path={`wslog:${sel.rowid}:req`} />
                )}
                {tab === 'response' && <PayloadBody text={content?.response} loading={!content} path={`wslog:${sel.rowid}:rsp`} />}
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// 基本信息(DevTools Headers 风格的两列键值)
// 字段顺序与标签对齐 T100 原生页 awsq990 的列名(见 debug/wslog.go 顶部注释)
function InfoBody({ item }: { item: WSLogItem }) {
  const rows: [string, string][] = [
    ['服务名称', item.service],
    ['作业', item.job],
    ['服务程序序号', item.pid],
    ['处理结果', item.code],
    ['开始时间', item.start],
    ['结束时间', item.end],
    ['耗时(s)', item.duration],
    ['sso时间(秒)', item.sso],
    ['发起端', item.origin],
    ['服务端', item.server],
    ['错误消息', item.errMsg],
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

// 报文内容:与源码编辑器同一个 Monaco(同主题 = 同背景色)。
// Request 页可编辑(改过的入参可直接用于重放),Response 页只读。
function PayloadBody({ text, loading, path, editable, onChange }: {
  text?: string; loading: boolean; path?: string; editable?: boolean; onChange?: (v: string) => void
}) {
  return (
    <PayloadEditor
      text={text}
      loading={loading}
      path={path}
      editable={editable}
      onChange={onChange}
      emptyHint="无报文(超过入库大小上限且源文件已清理)"
    />
  )
}
