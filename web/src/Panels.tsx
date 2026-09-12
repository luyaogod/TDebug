// 右侧面板:运行/调试(VS Code 风格)+ 调用栈 / 变量监视 / 断点(一体化容器,分割线分区)
import * as React from 'react'
import * as AccordionPrimitive from '@radix-ui/react-accordion'
import { useEffect, useRef, useState } from 'react'
import { Eye, EyeOff, Play, Ruler } from 'lucide-react'
import { useStore } from './store'
import { Badge, Checkbox, Input } from './ui'
import { parseFglTree, type TNode } from './fglparse'
import { VarTreeNodes } from './VarTreeUi'

// 运行/调试区(VS Code Run and Debug 同款):绿色运行按钮 + 目标输入框。
// 输入作业编号或程序名(模块自动解析);也支持「模块/作业」显式指定模块。
// 会话进行中:显示当前作业编号 + 行号校准按钮(协议行号与源码偏移时手动触发)
function LaunchSection() {
  const launch = useStore((s) => s.launch)
  const launching = useStore((s) => s.launching)
  const sessionId = useStore((s) => s.sessionId)
  const prog = useStore((s) => s.prog)
  const state = useStore((s) => s.state)
  const sessionEnv = useStore((s) => s.sessionEnv)
  const calibrate = useStore((s) => s.calibrate)
  const [v, setV] = useState(() => localStorage.getItem('tdebug.launchTarget') || 'bsft001_wf')
  const doLaunch = () => {
    const t = v.trim()
    if (!t || launching) return
    localStorage.setItem('tdebug.launchTarget', t)
    const i = t.indexOf('/')
    if (i > 0) void launch(t.slice(0, i).trim(), t.slice(i + 1).trim())
    else void launch('', t)
  }
  // 单一常驻会话:会话空闲(idle)时仍显示启动输入,可复用宿主启动/换作业
  const inRun = !!sessionId && state !== 'idle' && state !== 'exit'
  return (
    <div className="shrink-0 border-b border-border">
      <div className="flex h-8 items-center px-2.5 text-xs font-medium text-muted-foreground">运行</div>
      {inRun ? (
        <div className="flex items-center gap-1 px-1.5 pb-1.5">
          <span
            className="min-w-0 flex-1 truncate bg-accent/40 px-1.5 py-0.5 font-mono text-xs text-foreground"
            title={prog || sessionId || ''}
          >
            {prog || (state === 'loading' ? '连接中…' : sessionEnv || '会话')}
          </span>
          <button
            title={state === 'stopped' ? '行号校准:协议行号与源码错位时点击对齐' : '行号校准(需停站后点击)'}
            disabled={state !== 'stopped'}
            onClick={() => void calibrate()}
            className="p-1 transition-colors hover:bg-accent/60 disabled:pointer-events-none disabled:opacity-30"
          >
            <Ruler className="h-4 w-4 text-sky-600 dark:text-sky-400" />
          </button>
        </div>
      ) : (
        <div className="px-1.5 pb-1.5">
          {state === 'idle' && (
            <div className="pb-1 text-[11px] text-muted-foreground">
              {sessionEnv ? `会话空闲(${sessionEnv}),可直接启动调试或换作业` : '会话空闲,可直接启动调试'}
            </div>
          )}
          <div className="flex items-center gap-1">
            <button
              title="启动调试会话(Enter 同效)"
              disabled={launching || !v.trim()}
              onClick={doLaunch}
              className="p-1 transition-colors hover:bg-accent/60 disabled:pointer-events-none disabled:opacity-30"
            >
              <Play className="h-4 w-4 text-green-600 dark:text-green-500" fill="currentColor" />
            </button>
            <Input
              value={v}
              onChange={(e) => setV(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') doLaunch() }}
              placeholder="作业编号,如 bsft001_wf 或 asf/bsft001_wf"
              className="h-6 flex-1 px-1.5 text-xs"
            />
          </div>
        </div>
      )}
    </div>
  )
}

// shadcn 风格 Accordion 基础组件(Radix)
const Accordion = AccordionPrimitive.Root

const AccordionItem = React.forwardRef<
  React.ElementRef<typeof AccordionPrimitive.Item>,
  React.ComponentPropsWithoutRef<typeof AccordionPrimitive.Item>
>(({ className, ...props }, ref) => (
  <AccordionPrimitive.Item
    ref={ref}
    className={`flex min-h-0 flex-col overflow-hidden border-b border-border data-[state=closed]:flex-none last:border-b-0 ${className || ''}`}
    {...props}
  />
))
AccordionItem.displayName = 'AccordionItem'

const AccordionContent = React.forwardRef<
  React.ElementRef<typeof AccordionPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof AccordionPrimitive.Content>
>(({ className, children, ...props }, ref) => (
  <AccordionPrimitive.Content
    ref={ref}
    className={`min-h-0 flex-1 overflow-auto ${className || ''}`}
    {...props}
  >
    {children}
  </AccordionPrimitive.Content>
))
AccordionContent.displayName = 'AccordionContent'

// 面板头行:左侧固定位(自动开关或等宽占位),右侧手风琴触发区(整块点击开合)
function PanelHeader({ leading, children }: { leading?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex h-8 shrink-0 items-stretch">
      {leading}
      <AccordionPrimitive.Trigger
        className="flex h-full min-w-0 flex-1 items-center justify-between gap-2 pr-2.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent/50 hover:text-foreground [&[data-state=open]>svg]:rotate-180"
      >
        <span className="min-w-0 flex-1 truncate pl-1">{children}</span>
        <svg
          xmlns="http://www.w3.org/2000/svg"
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          className="shrink-0 text-muted-foreground transition-transform duration-200"
        >
          <path d="m6 9 6 6 6-6" />
        </svg>
      </AccordionPrimitive.Trigger>
    </div>
  )
}

// 面板「自动」开关:开(实心眼)= 停站后自动抓取该面板数据(调用栈/自动变量;
// 都要经调试会话逐条发命令,全开时步进卡)。默认关(空心眼):停站不再自动调度,
// 需要时点开,立即生效并自动展开面板。
function AutoSwitch({ on, onToggle, onTip, offTip }: {
  on: boolean; onToggle: () => void; onTip: string; offTip: string
}) {
  const Icon = on ? Eye : EyeOff
  return (
    <button
      title={on ? onTip : offTip}
      aria-pressed={on}
      onClick={onToggle}
      className={`flex w-6 shrink-0 items-center justify-center transition-colors hover:bg-accent/50 ${
        on ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground hover:text-foreground'
      }`}
    >
      <Icon className="h-3.5 w-3.5" />
    </button>
  )
}

export function RightPanels() {
  const stackAuto = useStore((s) => s.stackAuto)
  const autovarsAuto = useStore((s) => s.autovarsAuto)
  const toggleStackAuto = useStore((s) => s.toggleStackAuto)
  const toggleAutovarsAuto = useStore((s) => s.toggleAutovarsAuto)
  // 手风琴开合(受控):默认只展开「变量监视/断点」;调用栈/自动变量随自动开关
  // 联动——开则展开(数据开始自动刷新),关(默认)则收起,需要时手动点开看存量
  const [open, setOpen] = useState<string[]>(() => {
    const v = ['watches', 'bps']
    if (stackAuto) v.push('stack')
    if (autovarsAuto) v.push('autovars')
    return v
  })
  useEffect(() => {
    setOpen((o) => (stackAuto ? (o.includes('stack') ? o : [...o, 'stack']) : o.filter((x) => x !== 'stack')))
  }, [stackAuto])
  useEffect(() => {
    setOpen((o) => (autovarsAuto ? (o.includes('autovars') ? o : [...o, 'autovars']) : o.filter((x) => x !== 'autovars')))
  }, [autovarsAuto])

  return (
    // 一体化侧栏面板(VS Code 经典):与侧栏同底色、无外框无圆角,区块间用分割线区分
    <div className="flex h-full min-h-0 w-full flex-col overflow-hidden bg-background">
      <LaunchSection />
      <Accordion type="multiple" value={open} onValueChange={setOpen} className="flex h-full min-h-0 w-full flex-col">
        <AccordionItem value="stack">
          <PanelHeader leading={<AutoSwitch on={stackAuto} onToggle={() => toggleStackAuto()}
            onTip="自动刷新已开启:每次停站抓取调用栈(点击关闭,减少自动调度卡顿)"
            offTip="自动刷新已关闭(默认):停站后不再抓调用栈;点击开启" />}>
            <StackTitle />
          </PanelHeader>
          <AccordionContent>
            <StackBody />
          </AccordionContent>
        </AccordionItem>

        <AccordionItem value="autovars">
          <PanelHeader leading={<AutoSwitch on={autovarsAuto} onToggle={() => toggleAutovarsAuto()}
            onTip="自动求值已开启:每次停站求值源码窗变量并刷新本面板(点击关闭,减少自动调度卡顿)"
            offTip="自动求值已关闭(默认):停站后不再求值自动变量;点击开启" />}>
            <AutovarsTitle />
          </PanelHeader>
          <AccordionContent>
            <AutovarsBody />
          </AccordionContent>
        </AccordionItem>

        <AccordionItem value="watches">
          <PanelHeader leading={<span className="block w-6 shrink-0" aria-hidden />}>
            <WatchesTitle />
          </PanelHeader>
          <AccordionContent>
            <WatchesBody />
          </AccordionContent>
        </AccordionItem>

        <AccordionItem value="bps">
          <PanelHeader leading={<span className="block w-6 shrink-0" aria-hidden />}>
            <BpsTitle />
          </PanelHeader>
          <AccordionContent>
            <BpsBody />
          </AccordionContent>
        </AccordionItem>
      </Accordion>
    </div>
  )
}

// ---- 各面板标题(折叠时也始终可见,计数实时) ----

function StackTitle() {
  const n = useStore((s) => s.frames.length)
  return <span className="pl-0.5">调用栈 ({n})</span>
}

function AutovarsTitle() {
  const n = useStore((s) => s.autovars.length)
  return <span className="pl-0.5">自动变量 ({n})</span>
}

function WatchesTitle() {
  const n = useStore((s) => s.watches.length)
  return <span className="pl-0.5">变量监视 ({n})</span>
}

function BpsTitle() {
  const n = useStore((s) => s.breakpoints.length)
  return <span className="pl-0.5">断点 ({n})</span>
}

// ---- 各面板内容 ----

function StackBody() {
  const frames = useStore((s) => s.frames)
  const selected = useStore((s) => s.selectedFrame)
  const selectFrame = useStore((s) => s.selectFrame)
  const stopped = useStore((s) => s.state === 'stopped')
  if (frames.length === 0) return null
  return (
    <div>
      {frames.map((f) => (
        <div
          key={f.idx}
          className={`cursor-pointer border-b border-border/60 px-2 py-1 text-xs hover:bg-accent/40 ${
            selected === f.idx ? 'bg-sky-500/10' : ''
          } ${!stopped ? 'opacity-60' : ''}`}
          title={stopped ? `点击切到该帧上下文(print/locals 随之切换)` : '停站后可切换栈帧'}
          onClick={() => stopped && void selectFrame(f.idx)}
        >
          <span className="mr-1.5 text-muted-foreground">#{f.idx}</span>
          <span className="text-sky-600 dark:text-sky-400">{f.func}</span>
          <span className="ml-1.5 text-muted-foreground">{f.file}:{f.line}</span>
        </div>
      ))}
    </div>
  )
}

// 条目视图构建:RECORD/ARRAY 文本解析成树,标量保持原文本(监视/自动变量共用)
interface WatchView { expr: string; error?: string; text?: string; root?: TNode }

function buildVarView(expr: string, value?: string, error?: string): WatchView {
  if (error) return { expr, error }
  const kids = parseFglTree(value || '')
  if (kids) {
    const record = kids.some((k) => !k.name.startsWith('['))
    return { expr, root: { name: expr, open: false, children: kids, type: record ? 'RECORD' : `ARRAY[${kids.length}]` } }
  }
  return { expr, text: value }
}

// 自动变量:停站后从当前源码窗自动提取变量并求值(只读,可一键转为监视)
function AutovarsBody() {
  const autovars = useStore((s) => s.autovars)
  const addWatch = useStore((s) => s.addWatch)
  const [views, setViews] = useState<WatchView[]>([])
  useEffect(() => {
    setViews(autovars.map((v) => buildVarView(v.expr, v.value)))
  }, [autovars])
  if (autovars.length === 0) {
    return null
  }
  return (
    <div>
      {views.map((v) => (
        <div key={v.expr} className="group flex items-start gap-1 border-b border-border/60 px-2 py-1 text-xs">
          <button
            className="shrink-0 text-muted-foreground opacity-0 transition-opacity hover:text-emerald-600 dark:hover:text-emerald-400 group-hover:opacity-100"
            title="加入变量监视"
            onClick={() => void addWatch(v.expr)}
          >
            +
          </button>
          <div className="min-w-0 flex-1">
            {v.root ? (
              <VarTreeNodes nodes={[v.root]} />
            ) : (
              <>
                <div className="text-muted-foreground">{v.expr}</div>
                <div className="whitespace-pre-wrap break-all text-emerald-600 dark:text-emerald-400">{v.text}</div>
              </>
            )}
          </div>
        </div>
      ))}
    </div>
  )
}

function WatchesBody() {
  const watches = useStore((s) => s.watches)
  const removeWatch = useStore((s) => s.removeWatch)
  const doPrint = useStore((s) => s.doPrint)
  const [expr, setExpr] = useState('')
  const [views, setViews] = useState<WatchView[]>([])
  useEffect(() => {
    setViews(watches.map((w) => buildVarView(w.expr, w.value, w.error)))
  }, [watches])
  return (
    <div>
      <div className="flex gap-1 p-1.5">
        <Input
          value={expr}
          onChange={(e) => setExpr(e.target.value)}
          onKeyDown={(e) => {
            // 回车即求值:doPrint 会先 print 显示一次,再自动加入监视列表
            if (e.key === 'Enter' && expr.trim()) { void doPrint(expr.trim()); setExpr('') }
          }}
          placeholder="表达式,如 lp_str / g_qryparam.*(回车求值)"
          className="h-7 flex-1 text-xs"
        />
      </div>
      {views.map((v) => (
        <div key={v.expr} className="flex items-start gap-1 border-b border-border/60 px-2 py-1 text-xs">
          <button className="shrink-0 text-muted-foreground hover:text-red-600 dark:hover:text-red-400" onClick={() => removeWatch(v.expr)}>×</button>
          <div className="min-w-0 flex-1">
            {v.root ? (
              <VarTreeNodes nodes={[v.root]} />
            ) : (
              <>
                <div className="text-muted-foreground">{v.expr}</div>
                <div className={`whitespace-pre-wrap break-all ${v.error ? 'text-red-600 dark:text-red-400' : 'text-emerald-600 dark:text-emerald-400'}`}>
                  {v.error || v.text}
                </div>
              </>
            )}
          </div>
        </div>
      ))}
    </div>
  )
}

function BpsBody() {
  const breakpoints = useStore((s) => s.breakpoints)
  const removeBreakpoint = useStore((s) => s.removeBreakpoint)
  const toggleBPEnabled = useStore((s) => s.toggleBPEnabled)
  const jumpToBp = useStore((s) => s.jumpToBp)
  if (breakpoints.length === 0) return null
  return (
    <div>
      {breakpoints.map((b) => (
        <div key={b.num} className={`flex items-center gap-2 border-b border-border/60 px-2 py-1 text-xs ${b.enabled ? '' : 'opacity-50'}`}>
          <Checkbox
            checked={b.enabled}
            onCheckedChange={(v) => void toggleBPEnabled(b.num, v === true)}
            className="cursor-pointer"
            title={b.enabled ? '取消勾选禁用断点' : '勾选启用断点'}
          />
          <span
            className="min-w-0 flex-1 cursor-pointer truncate text-foreground hover:text-sky-700 dark:hover:text-sky-300 hover:underline"
            title={`跳转到 ${b.file}:${b.line}`}
            onClick={() => void jumpToBp(b)}
          >
            {b.func ? <span className="text-sky-600 dark:text-sky-400">{b.func} </span> : null}
            {b.file}:{b.line}
            {!b.enabled && <span className="ml-1 text-[10px] text-muted-foreground">(已禁用)</span>}
          </span>
          <button className="text-muted-foreground hover:text-red-600 dark:hover:text-red-400" onClick={() => void removeBreakpoint(b.num)}>×</button>
        </div>
      ))}
    </div>
  )
}

export function TimelinePanel() {
  const [tab, setTab] = useState<'timeline' | 'raw'>('raw')
  const timeline = useStore((s) => s.timeline)
  const rawLog = useStore((s) => s.rawLog)
  const sessionId = useStore((s) => s.sessionId)
  const state = useStore((s) => s.state)
  const sendRaw = useStore((s) => s.sendRaw)
  const bodyRef = useRef<HTMLDivElement | null>(null)
  const [cmd, setCmd] = useState('')
  const [hist, setHist] = useState<string[]>([])
  const [histIdx, setHistIdx] = useState(-1)
  // 新条目到达时自动滚动到底部(最新操作始终可见)
  useEffect(() => {
    const el = bodyRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [tab, timeline.length, rawLog.length])
  const kindTone: Record<string, string> = {
    stop: 'text-yellow-600 dark:text-yellow-400', warn: 'text-red-600 dark:text-red-400', command: 'text-sky-600 dark:text-sky-400', info: 'text-muted-foreground',
  }
  // fgldb 命令直通输入(复刻原版 fgldeb Ctrl+D 子画面);仅停站时可发
  const canSend = !!sessionId && state === 'stopped'
  const submit = () => {
    const c = cmd.trim()
    if (!c || !canSend) return
    setHist((h) => [...h.filter((x) => x !== c), c].slice(-50))
    setHistIdx(-1)
    setCmd('')
    void sendRaw(c)
  }
  const onCmdKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') { e.preventDefault(); submit(); return }
    // ↑/↓ 翻命令历史(最新在末尾,↓ 可回到空)
    if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
      e.preventDefault()
      if (hist.length === 0) return
      let idx = histIdx
      if (e.key === 'ArrowUp') idx = histIdx < 0 ? hist.length - 1 : Math.max(0, histIdx - 1)
      else idx = histIdx < 0 ? -1 : (histIdx + 1 >= hist.length ? -1 : histIdx + 1)
      setHistIdx(idx)
      setCmd(idx >= 0 ? hist[idx] : '')
    }
  }
  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      {/* 面板页签(VS Code PROBLEMS/OUTPUT 式):纯文字 + 活动页签主色下划线压住分割线 */}
      <div className="flex h-8 shrink-0 items-center gap-1 border-b border-border px-1.5">
        {([['raw', `原始协议流 (${rawLog.length})`], ['timeline', '操作时间线']] as const).map(([k, label]) => (
          <button key={k}
            className={`relative flex h-full items-center px-2.5 text-xs transition-colors ${
              tab === k ? 'text-foreground' : 'text-muted-foreground hover:text-foreground'
            }`}
            onClick={() => setTab(k)}
          >
            {label}
            {tab === k && <span className="absolute inset-x-0 bottom-0 h-px bg-primary" />}
          </button>
        ))}
      </div>
      <div ref={bodyRef} className="min-h-0 flex-1 overflow-auto p-1 font-mono text-[11px] leading-5">
        {tab === 'timeline' && (
          <>
            {timeline.length === 0 && <div className="p-2 text-muted-foreground">暂无记录</div>}
            {timeline.map((t, i) => (
              <div key={i} className="flex gap-2">
                <span className="shrink-0 text-muted-foreground">{t.time}</span>
                <Badge tone={t.origin === 'ai' ? 'blue' : t.origin === 'human' ? 'green' : 'gray'} className="mt-0.5 h-4 shrink-0">
                  {t.origin === 'ai' ? 'AI' : t.origin === 'human' ? '人' : '系统'}
                </Badge>
                <span className={`min-w-0 ${kindTone[t.kind] || 'text-muted-foreground'}`}>{t.text}</span>
              </div>
            ))}
          </>
        )}
        {tab === 'raw' && (
          <>
            {rawLog.map((l, i) => (
              <div key={i} className="whitespace-pre-wrap break-all text-muted-foreground">{l || ' '}</div>
            ))}
          </>
        )}
      </div>
      {tab === 'raw' && (
        // 命令输入(VS Code Debug Console 式):> 提示符 + 无边框输入行,回车即发送
        <div className="flex h-8 shrink-0 items-center gap-1.5 border-t border-border px-2.5">
          <span className="shrink-0 font-mono text-xs leading-none text-sky-600 dark:text-sky-400" title="fgldb 命令直通">&gt;</span>
          <input
            value={cmd}
            onChange={(e) => setCmd(e.target.value)}
            onKeyDown={onCmdKey}
            disabled={!canSend}
            placeholder={canSend ? 'fgldb 命令,如 print lp_str / info breakpoints(↑↓ 历史),回车发送' : '需停站后才能发送命令'}
            className="h-6 min-w-0 flex-1 bg-transparent font-mono text-xs text-foreground placeholder:text-muted-foreground focus:outline-none disabled:opacity-50"
          />
        </div>
      )}
    </div>
  )
}
