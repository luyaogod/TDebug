import { create } from 'zustand'
import { api, type Event, type StopInfo, type Breakpoint, type Frame, type VarItem, type WSLogItem, type WSLogContent, type WSTestResult } from './api'

// timeline 条目(人/AI/系统 的操作与事件,可审计)
export interface TimelineItem {
  time: string
  origin: 'system' | 'human' | 'ai'
  text: string
  kind: 'info' | 'stop' | 'command' | 'warn'
}

interface Watch { expr: string; value?: string; error?: string }

// 浏览页签:静态打开的源码文件(如 Ctrl+点击跳函数),与调试页互不干扰
export interface SrcTab {
  key: string
  file: string // DVM 文件名(如 s_apmt520_conf_chk.4gl)
  content: string
  path: string
  line?: number // 打开时定位行
  loading: boolean
  missing?: boolean // 无源码(只有 42m)
}

interface Store {
  // 连接
  wsConnected: boolean
  // 会话
  sessionId: string | null
  module: string
  prog: string
  state: string // loading|stopped|running|idle|exit|''
  sessionEnv: string // 会话所属环境名(设置页 envs;idle/状态栏/会话列表显示)
  started: boolean // 程序是否已 run 过(入口停站时步进不可用)
  stop: StopInfo | null
  holdingSeconds: number
  launching: boolean
  launchError: string // 启动失败显眼报错(编辑器外红色横幅,空 = 正常)
  // UI 面板显隐
  showRight: boolean
  showBottom: boolean
  rightView: 'debug' | 'outline' | 'session' // 右侧边栏当前 sheet(会话/调试面板/大纲),默认落「会话」
  // 调用栈/自动变量面板「自动」开关(手风琴头部图标,默认关):停站后是否自动抓取该数据
  // (两者都要经调试会话逐条发命令,自动调度越多步进越卡;关掉后仅手动/开关开启时取)
  stackAuto: boolean
  autovarsAuto: boolean
  // 最近一次接口日志重放(wslogs「调试此调用」)的日志 rowid:重放会话点「重新开始」
  // 时按同一日志重放(报文参数在后端),而不是普通启动丢参数;null = 普通作业调试
  lastReplayRowid: string | null
  // 数据
  breakpoints: Breakpoint[]
  adjustedBps: Record<number, number> // 点击行号 → 实际注册断点编号(fgldb 会把非可执行行的断点自动下移)
  frames: Frame[]
  watches: Watch[]
  autovars: VarItem[] // 停站自动变量(当前源码窗内变量的自动求值)
  selectedFrame: number // 当前选中栈帧(-1 = 未选)
  backendDead: string // 后端死亡/程序退出原因(空 = 正常)
  timeline: TimelineItem[]
  rawLog: string[]
  runProg: string // gzzz_t 解析出的实体程序(源码命名/预取用);空 = 与 prog 相同
  // 视图与接口日志(VS Code 活动栏切换)
  view: 'debug' | 'wslogs' | 'wstest' | 'settings'
  theme: 'dark' | 'light' // 外观主题(html.dark 挂点,localStorage tdebug.theme 持久化)
  // 服务测试(复刻 awsq990 集成服务测试)
  wsTestMode: string // 1/2 awsp900, 3 awsp920, 4 awsp940, 5 awsp930
  wsTestUrl: string
  wsTestBody: string
  wsTestSoap: boolean
  wsTestResult: WSTestResult | null
  wsTestRunning: boolean
  wsTestErr: string
  wsTestHistory: { time: string; url: string; httpCode: number; durationSec: number; response: string }[]
  wsLogs: WSLogItem[]
  wsLogsLoading: boolean
  wsLogsPage: number
  wsLogsHasMore: boolean
  wsLogSel: WSLogItem | null
  wsLogContent: WSLogContent | null
  wsLogTab: 'info' | 'request' | 'response'
  wsLogErr: string
  // 源码
  sourceContent: string
  sourcePath: string
  sourceDVM: string // 当前已加载源码对应的 DVM 模块文件名(无源码模块也记录,避免错文件高亮)
  currentLine: number
  loadingSource: boolean
  lineOffset: number // 行号校准偏移:DVM 行号 - 磁盘行号(>0 时 Monaco 顶部前插 offset 行对齐协议流)
  // 源码多页签:调试页固定第一个(跟随停站),浏览页为静态打开的其它源码文件
  tabs: SrcTab[]
  activeTab: string // 'debug' | tabs[].key
  revealReq: { key: string; line: number; seq: number } | null // 定位信号:哪个页签滚到哪行(唯一驱动视口滚动)

  // actions
  setWsConnected: (b: boolean) => void
  toggleRight: () => void
  toggleBottom: () => void
  toggleStackAuto: () => void
  toggleAutovarsAuto: () => void
  // 接口日志重放启动(wslogs「调试此调用」与重放会话「重新开始」共用同一上下文)
  replayStart: (rowid: string) => Promise<void>
  setRightView: (v: 'debug' | 'outline' | 'session') => void
  pushRaw: (line: string) => void
  sendRaw: (cmd: string) => Promise<void>
  pushTimeline: (item: Omit<TimelineItem, 'time'>) => void
  onEvent: (ev: Event) => void
  setView: (v: 'debug' | 'wslogs' | 'wstest' | 'settings') => void
  setLaunchError: (msg: string) => void
  setTheme: (t: 'dark' | 'light') => void
  setActiveTab: (key: string) => void
  reveal: (key: string, line: number) => void
  closeTab: (key: string) => void
  openSourceTab: (file: string, line?: number) => Promise<void>
  locate: (word: string) => Promise<{ file: string; line: number }>
  calibrate: () => Promise<void>
  setWsTest: (p: { mode?: string; url?: string; body?: string; soap?: boolean; result?: WSTestResult | null }) => void
  runWsTest: () => Promise<void>
  loadWsLogs: (service: string, onlyFail: boolean, page?: number, startFrom?: string, startTo?: string) => Promise<void>
  selectWsLog: (item: WSLogItem) => Promise<void>
  setWsLogTab: (t: 'info' | 'request' | 'response') => void
  closeWsLogDetail: () => void
  replayDebug: (item: WSLogItem) => Promise<void>
  launch: (module: string, prog: string) => Promise<void>
  refreshSnapshot: () => Promise<void>
  refreshSource: (file?: string, line?: number) => Promise<void>
  refreshFrames: () => Promise<Frame[]>
  refreshWatches: () => Promise<void>
  addWatch: (expr: string) => Promise<void>
  removeWatch: (expr: string) => void
  selectFrame: (idx: number) => Promise<void>
  toggleBPEnabled: (num: number, enabled: boolean) => Promise<void>
  jumpToBp: (b: Breakpoint) => Promise<void>
  runToCursor: (line: number) => Promise<void>
  control: (action: string, arg?: string) => Promise<void>
  toggleBreakpoint: (line: number) => Promise<void>
  removeBreakpoint: (num: number) => Promise<void>
  quit: () => Promise<void>
  restart: () => Promise<void>
  doPrint: (expr: string) => Promise<void>
  adoptExisting: () => Promise<void>
  // 会话管理(右侧「会话」sheet):切换/重启/结束常驻会话(会先结束当前 debug)
  sessionOp: (op: 'close' | 'restart' | 'switch', env?: string) => Promise<void>
  syncFromSessions: () => Promise<void>
}

let snapTimer: number | undefined
let holdTimer: number | undefined

function now() { return new Date().toLocaleTimeString('zh-CN', { hour12: false }) }

// 跳到 MAIN 语句行(无停站位置时给用户一个可点断点的起点)
function jumpToMain(set: (p: Partial<Store>) => void, get: () => Store) {
  if (get().currentLine > 0) return
  const lines = get().sourceContent.split('\n')
  for (let i = 0; i < lines.length; i++) {
    if (/^\s*MAIN\b/i.test(lines[i])) { set({ currentLine: i + 1 }); break }
  }
}

export const useStore = create<Store>((set, get) => ({
  wsConnected: false,
  sessionId: null, module: '', prog: '', state: '', sessionEnv: '', started: false, stop: null, holdingSeconds: 0,
  launching: false, launchError: '', showRight: true, showBottom: true,
  // 面板开关默认关(localStorage tdebug.stackAuto / tdebug.autovarsAuto 持久化)
  stackAuto: localStorage.getItem('tdebug.stackAuto') === '1',
  autovarsAuto: localStorage.getItem('tdebug.autovarsAuto') === '1',
  lastReplayRowid: null,
  // 右侧边栏默认落在「会话」(先选环境再调试);Tab 顺序见 App.tsx 右侧切换栏,不做持久化
  rightView: 'session',
  breakpoints: [], adjustedBps: {}, frames: [], watches: [], autovars: [], selectedFrame: -1, backendDead: '',
  timeline: [], rawLog: [],
  runProg: '',
  view: 'debug',
  theme: (localStorage.getItem('tdebug.theme') === 'light' ? 'light' : 'dark') as 'dark' | 'light',
  wsLogs: [], wsLogsLoading: false, wsLogsPage: 1, wsLogsHasMore: false,
  wsLogSel: null, wsLogContent: null, wsLogTab: 'info', wsLogErr: '',
  wsTestMode: '3', wsTestUrl: '', wsTestBody: '', wsTestSoap: false,
  wsTestResult: null, wsTestRunning: false, wsTestErr: '', wsTestHistory: [],
  sourceContent: '', sourcePath: '', sourceDVM: '', currentLine: 0, loadingSource: false, lineOffset: 0,
  tabs: [], activeTab: 'debug', revealReq: null,

  setWsConnected: (b) => set({ wsConnected: b }),
  toggleRight: () => set((st) => ({ showRight: !st.showRight })),
  toggleBottom: () => set((st) => ({ showBottom: !st.showBottom })),

  // 调用栈面板「自动」开关(默认关):开 → 停站后自动抓调用栈,已停站时立即抓一次
  toggleStackAuto: () => {
    const st = get()
    const on = !st.stackAuto
    localStorage.setItem('tdebug.stackAuto', on ? '1' : '0')
    set({ stackAuto: on })
    if (on && get().state === 'stopped') void get().refreshFrames()
  },
  // 自动变量面板「自动」开关(默认关):服务端停站后自动求值窗口变量才发生(见
  // 会话 SetAutovarsOn);开 → 下发当前会话(已停站时服务端立即补一次求值并推送)
  toggleAutovarsAuto: () => {
    const st = get()
    const on = !st.autovarsAuto
    localStorage.setItem('tdebug.autovarsAuto', on ? '1' : '0')
    set({ autovarsAuto: on })
    const sid = get().sessionId
    if (!sid) return
    void api.autovarsAuto(sid, on).catch(() => { /* 会话切换窗口:下次停站前由对齐逻辑重发 */ })
  },
  setRightView: (v) => set({ rightView: v }),

  pushRaw: (line) => set((st) => {
    const log = st.rawLog.length > 3000 ? st.rawLog.slice(-2000) : st.rawLog
    return { rawLog: [...log, line] }
  }),

  // 直接执行 fgldb 命令(复刻原版 fgldeb Ctrl+D「Input Debugger Command」的透传):
  // 命令与输出原样进协议流;状态类命令执行后刷新快照/栈,与原版行为一致
  sendRaw: async (cmd) => {
    const { sessionId } = get()
    if (!sessionId || !cmd.trim()) return
    get().pushRaw(`> ${cmd}`)
    try {
      const r = await api.raw(sessionId, cmd)
      for (const l of r.lines) get().pushRaw(l)
      const head = cmd.trim().split(/\s+/)[0].toLowerCase()
      if (['step', 'next', 'continue', 'run'].includes(head)) {
        // 程序会继续运行:走轮询等下一次停站
        pollUntilStopped(set, get)
      } else if (['break', 'tbreak', 'clear', 'delete', 'enable', 'disable', 'where', 'frame', 'finish', 'return'].includes(head)) {
        void get().refreshSnapshot()
        if (get().state === 'stopped') void get().refreshFrames()
      }
    } catch (e: any) {
      get().pushRaw(`[错误] ${e.message || String(e)}`)
    }
  },

  pushTimeline: (item) => set((st) => ({
    timeline: [...st.timeline.slice(-500), { ...item, time: now() }],
  })),

  onEvent: (ev) => {
    const st = get()
    if (st.sessionId && ev.sessionId && ev.sessionId !== st.sessionId) return
    switch (ev.type) {
      case 'output':
        st.pushRaw(ev.text || '')
        return
      case 'state':
        set({ state: ev.state || '' })
        if (ev.state === 'stopped') {
          // 停站后按面板开关自动刷新(异步,不阻塞):调用栈/自动变量默认关,监视保持
          if (get().stackAuto) void get().refreshFrames()
          void get().refreshWatches()
          startHoldTimer(set, get)
        }
        if (ev.state === 'running' || ev.state === 'idle' || ev.state === 'exit') {
          if (snapTimer) { clearInterval(snapTimer); snapTimer = undefined }
          stopHoldTimer()
          if (ev.state === 'running') set({ selectedFrame: -1 })
        }
        if (ev.state === 'idle') {
          // 本轮调试结束,宿主会话保留(idle):清运行现场,源码保留便于浏览/再次启动
          set({ started: false, stop: null, currentLine: 0, loadingSource: false, selectedFrame: -1, autovars: [], breakpoints: [], frames: [], adjustedBps: {} })
        }
        if (ev.state === 'exit') {
          st.pushTimeline({ origin: 'system', kind: 'warn', text: '会话已断开(结束会话/切换环境或连接中断)' })
          set({ loadingSource: false, currentLine: 0, started: false, stop: null })
          // 后端已移除该会话:按服务端现状对齐绑定(有常驻/新会话则重绑,没了即清),
          // 避免 sessionId 残留导致界面一直停在"会话进行中"(如 wslogs 重放被锁)
          void get().syncFromSessions()
        }
        return
      case 'dead':
        // 后端死亡/SSH 断开:forceExit 随后会发 state=exit
        set({ backendDead: ev.text || '调试后端连接断开' })
        st.pushTimeline({ origin: 'system', kind: 'warn', text: ev.text || '调试后端连接断开' })
        return
      case 'autovars':
        set({ autovars: ev.vars || [] })
        return
      case 'stopped': {
        // 跨文件停站:先收光标(源码未就位时不画停站行,防它在旧文件上错位),
        // refreshSource 拉到新源码后再一次性落位
        const nl = !!ev.stop?.file && ev.stop.file !== get().sourceDVM
        set({ stop: ev.stop || null, state: 'stopped', currentLine: nl ? 0 : (ev.stop?.line || get().currentLine), selectedFrame: -1, loadingSource: nl, activeTab: 'debug' })
        st.pushTimeline({
          origin: 'system', kind: 'stop',
          text: `停站[${ev.stop?.reason}] ${ev.stop?.file || ''}:${ev.stop?.line ?? ''} ${ev.stop?.func || ''}`,
        })
        // 停站文件缺失(如 SIGINT 中断块无文件头)时,用栈顶帧的真实文件跟随
        void (async () => {
          let file = ev.stop?.file
          let frameLine = 0
          if (!file && get().stackAuto) {
            const frames = await get().refreshFrames()
            file = frames[0]?.file
            frameLine = frames[0]?.line ?? 0
          }
          await get().refreshSource(file, frameLine || ev.stop?.line || 0)
          await get().refreshWatches()
          await get().refreshSnapshot()
        })()
        startHoldTimer(set, get)
        return
      }
      case 'watchdog':
        st.pushTimeline({ origin: 'system', kind: 'warn', text: ev.text || '看门狗触发' })
        return
      case 'ai_action':
        st.pushTimeline({ origin: 'ai', kind: 'command', text: ev.text || '' })
        return
      case 'log': {
        const text = ev.text || ''
        st.pushTimeline({ origin: 'system', kind: 'info', text })
        // 后端异步启动失败(HTTP 已返回 200,失败发生在 goroutine):会话随即被移除,
        // 必须立刻退出加载态、清掉会话引用并给出显眼报错,否则编辑器永久转圈、
        // 运行区还停留在"会话进行中"而无法再次启动
        if (text.startsWith('启动失败')) {
          if (snapTimer) { clearInterval(snapTimer); snapTimer = undefined }
          stopHoldTimer()
          set({ launchError: text, sessionId: null, state: '', loadingSource: false, stop: null, currentLine: 0 })
        }
        return
      }
    }
  },

  setView: (v) => set({ view: v }),
  setLaunchError: (msg) => set({ launchError: msg }),
  setTheme: (t) => {
    localStorage.setItem('tdebug.theme', t)
    document.documentElement.classList.toggle('dark', t === 'dark')
    set({ theme: t })
  },

  setActiveTab: (key) => set({ activeTab: key }),

  reveal: (key, line) => {
    if (!(line > 0)) return
    set((st) => ({ revealReq: { key, line, seq: (st.revealReq?.seq ?? 0) + 1 }, currentLine: key === 'debug' ? line : st.currentLine }))
  },

  locate: async (word) => {
    const { sessionId } = get()
    if (!sessionId) throw new Error('无调试会话')
    return api.locate(sessionId, word)
  },

  // 行号校准:检测 fgldb(DVM)行号与磁盘源码的偏移,前插 offset 行让 Monaco 行号对齐协议流。
  // 用户在协议流与源码对不上时主动触发(不同文件/编译版本的偏移可能不同)
  calibrate: async () => {
    const { sessionId, state } = get()
    if (!sessionId) return
    if (state !== 'stopped') {
      get().pushTimeline({ origin: 'system', kind: 'warn', text: '行号校准:需停站后才能检测偏移' })
      return
    }
    try {
      const { offset } = await api.calibrate(sessionId)
      if (offset < 0) {
        get().pushTimeline({ origin: 'system', kind: 'warn', text: `行号校准:源码比编译版本多 ${-offset} 行,无法前插对齐,请在服务器重新编译或核对源码版本` })
        return
      }
      set({ lineOffset: offset })
      get().pushTimeline({ origin: 'human', kind: 'info', text: offset > 0 ? `行号校准完成:协议行号比源码多 ${offset} 行,已前插对齐` : '行号校准完成:行号无偏移' })
      const st = get()
      if (st.stop?.line) get().reveal('debug', st.stop.line)
    } catch (e: any) {
      get().pushTimeline({ origin: 'system', kind: 'warn', text: `行号校准失败: ${e.message || String(e)}` })
    }
  },

  closeTab: (key) => set((st) => {
    const idx = st.tabs.findIndex((t) => t.key === key)
    if (idx < 0) return {}
    const tabs = st.tabs.filter((t) => t.key !== key)
    if (st.activeTab !== key) return { tabs }
    return { tabs, activeTab: tabs[idx] ? tabs[idx].key : tabs[idx - 1] ? tabs[idx - 1].key : 'debug' }
  }),

  openSourceTab: async (file, line) => {
    const key = 'src:' + file
    const st = get()
    const existing = st.tabs.find((t) => t.key === key)
    if (existing) {
      // 已开:激活;带行号才重新定位(纯激活保持离开时视口)
      set({ activeTab: key, tabs: line ? st.tabs.map((t) => t.key === key ? { ...t, line } : t) : st.tabs })
      if (line) get().reveal(key, line)
      return
    }
    if (!st.sessionId) return
    const tab: SrcTab = { key, file, content: '', path: '', line, loading: true }
    set({ tabs: [...st.tabs, tab], activeTab: key })
    try {
      const { source } = await api.sourceByFile(st.sessionId, file, st.module)
      set((s2) => ({ tabs: s2.tabs.map((t) => t.key === key ? { ...t, content: source.content, path: source.path, loading: false } : t) }))
      if (line) get().reveal(key, line)
    } catch {
      // 无源码(只有 42m 等):页签保留并标记,内容区给提示
      set((s2) => ({ tabs: s2.tabs.map((t) => t.key === key ? { ...t, loading: false, missing: true } : t) }))
    }
  },

  setWsTest: (p) => {
    const patch: Partial<Store> = {}
    if (p.mode !== undefined) patch.wsTestMode = p.mode
    if (p.url !== undefined) patch.wsTestUrl = p.url
    if (p.body !== undefined) patch.wsTestBody = p.body
    if (p.soap !== undefined) patch.wsTestSoap = p.soap
    if (p.result !== undefined) patch.wsTestResult = p.result
    set(patch)
  },

  runWsTest: async () => {
    const st = get()
    set({ wsTestRunning: true, wsTestErr: '' })
    try {
      const { result } = await api.wsTest(st.wsTestMode, st.wsTestUrl, st.wsTestBody, st.wsTestSoap)
      set({ wsTestResult: result })
      if (result.httpCode > 0) {
        const entry = {
          time: new Date().toLocaleTimeString('zh-CN', { hour12: false }),
          url: st.wsTestUrl || '(默认地址)',
          httpCode: result.httpCode, durationSec: result.durationSec, response: result.response,
        }
        set((s) => ({ wsTestHistory: [entry, ...s.wsTestHistory].slice(0, 10) }))
      }
    } catch (e: any) {
      set({ wsTestErr: e.message || String(e) })
    } finally {
      set({ wsTestRunning: false })
    }
  },

  loadWsLogs: async (service, onlyFail, page = 1, startFrom = '', startTo = '') => {
    set({ wsLogsLoading: true, wsLogErr: '' })
    try {
      const r = await api.wsLogs(service.trim(), onlyFail, page, startFrom, startTo)
      set({ wsLogs: r.items || [], wsLogsHasMore: !!r.hasMore, wsLogsPage: page, wsLogSel: null, wsLogContent: null })
    } catch (e: any) {
      set({ wsLogErr: e.message || String(e), wsLogs: [] })
    } finally {
      set({ wsLogsLoading: false })
    }
  },

  selectWsLog: async (item) => {
    set({ wsLogSel: item, wsLogContent: null, wsLogErr: '' })
    try {
      const { content } = await api.wsLogContent(item.rowid)
      set({ wsLogContent: content })
    } catch (e: any) {
      set({ wsLogErr: e.message || String(e) })
    }
  },

  setWsLogTab: (t) => set({ wsLogTab: t }),

  // 关闭日志详情面板:回到"只显示列表"的默认态
  closeWsLogDetail: () => set({ wsLogSel: null, wsLogContent: null }),

  replayDebug: async (item) => {
    await get().replayStart(item.rowid)
  },

  // 接口日志重放启动:立即切到 debug 页进 loading,再请求后端重放该日志
  // (后端按 rowid 重读报文并落临时文件,报文参数在 ArgsOverride 里,随会话保留)
  replayStart: async (rowid) => {
    set({ view: 'debug', wsLogErr: '', launching: true, launchError: '', state: 'loading' })
    try {
      // 已有会话在跑:先结束它(一次只能调一个作业;后端重放也会收口,双保险)
      const old = get().sessionId
      if (old) {
        set({ sessionId: null })
        void api.quit(old).catch(() => {})
      }
      const r = await api.wsLogDebug(rowid)
      const mod = r.module || ''
      const rp = r.runProg || r.prog || ''
      set({
        sessionId: r.sessionId, module: mod, prog: r.prog || rp,
        runProg: rp, state: 'loading', lastReplayRowid: rowid,
        timeline: [], rawLog: [], watches: [], autovars: [],
        backendDead: '', selectedFrame: -1, stop: null, breakpoints: [], frames: [],
        sourceContent: '', sourcePath: '', sourceDVM: '', currentLine: 0, loadingSource: true,
      })
      // 入口停站前保持加载态,源码由会话路径加载并定位 MAIN
      pollUntilStopped(set, get)
    } catch (e: any) {
      set({ wsLogErr: e.message || String(e), state: '', launchError: `启动失败: ${e.message}`, sessionId: null, loadingSource: false, stop: null })
    } finally {
      set({ launching: false })
    }
  },

  launch: async (module, prog) => {
    set({ launching: true, launchError: '', timeline: [], rawLog: [], watches: [], autovars: [], backendDead: '', selectedFrame: -1, lastReplayRowid: null })
    // 启动调试:编辑器进入加载态(转圈),入口停站定位 MAIN 后一次性显示源码,
    // 避免启动过程中内容跳来跳去
    set({ sourceContent: '', sourcePath: '', sourceDVM: '', currentLine: 0, loadingSource: true })
    try {
      const r = await api.launch(module, prog)
      // 作业编号解析:后端连 gzzz_t 后回读模块与实体程序(aint301_wf → aint302_wf)
      const mod = r.module || module
      const rp = r.runProg || prog
      set({ sessionId: r.sessionId, module: mod, prog, runProg: rp, state: 'loading' })
      get().pushTimeline({ origin: 'human', kind: 'command', text: `启动调试会话 ${mod}/${prog}` })
      alignAutoPrefs(set, get) // 自动变量开关下发到新会话(求值在服务端做)
      // 轮询直到入口停站(源码由会话路径加载并定位 MAIN)
      pollUntilStopped(set, get)
    } catch (e: any) {
      // 同步失败(如 prog 缺失/配置非法):HTTP 直接报错,同样必须退出加载态
      get().pushTimeline({ origin: 'system', kind: 'warn', text: `启动失败: ${e.message}` })
      set({ launchError: `启动失败: ${e.message}`, sessionId: null, state: '', loadingSource: false, stop: null, currentLine: 0 })
      throw e
    } finally {
      set({ launching: false })
    }
  },

  refreshSnapshot: async () => {
    const { sessionId } = get()
    if (!sessionId) return
    try {
      const snap = await api.snapshot(sessionId)
      // 注意:这里不更新 currentLine——下断点等操作也会触发快照刷新,
      // 焦点行只能由真正的停站事件/步进响应驱动,否则浏览位置会被拽回运行行
      set({
        state: snap.state, stop: snap.stop, breakpoints: snap.breakpoints || [],
        started: !!snap.started,
        holdingSeconds: snap.holdingSeconds || 0,
        sessionEnv: snap.env || get().sessionEnv,
        // 免模块启动时后端会按作业名解析模块,回读给前端(源码兜底路径依赖它)
        module: snap.module || get().module,
        runProg: snap.runProg || get().runProg,
      })
      if (snap.state === 'stopped' && !snapTimer) startHoldTimer(set, get)
      if (snap.state !== 'stopped' && snap.state !== 'loading' && snapTimer) {
        clearInterval(snapTimer); snapTimer = undefined
      }
    } catch { /* 会话可能已结束 */ }
  },

  refreshSource: async (file, line) => {
    const { sessionId, module, prog, runProg, sourceDVM } = get()
    if (!sessionId) return
    let f = file || get().stop?.file
    let entryMode = false
    if (!f) {
      // 入口停站:DVM 未上报文件名,按 T100 命名规则加载母版源码,便于直接点行号下断点
      // 注意:作业编号可能解析出不同名的实体程序(gzzz_t),母版跟实体程序走
      const rp = runProg || prog
      if (!rp) return
      f = `${module ? module + '_' : ''}${rp}.4gl`
      entryMode = true
    } else if (get().stop?.reason === 'entry') {
      // 入口停站:后端已给出真实源文件(转客制作业是 cpm_xxx.4gl),仍需定位 MAIN
      entryMode = true
    }
    if (f === sourceDVM) {
      // 同文件:行号直接落位
      if (line) set({ currentLine: line })
      return
    }
    // 跨文件停站:先收光标(currentLine=0)防它在旧文件上错位,源码到位后一次性落位
    // 行号偏移逐文件不同,切文件后需重新校准
    set({ loadingSource: true, currentLine: 0, lineOffset: 0 })
    try {
      const { source } = await api.sourceByFile(sessionId, f, module)
      set({ sourceContent: source.content, sourcePath: source.path, sourceDVM: f })
      const st = get()
      if (line) {
        set({ currentLine: line })
        // 跨文件停站(F11 步入/断点/运行到其它文件):源码到位后发定位信号,
        // 由 reveal effect 滚动视口到停站行;入口停站走下方 MAIN 定位
        if (!entryMode) get().reveal('debug', line)
      } else if (entryMode && st.currentLine === 0) {
        jumpToMain(set, get)
        st.pushTimeline({ origin: 'system', kind: 'info', text: '入口停站:已显示源码,点击行号下断点后点「继续 F5」开始' })
      }
      // 入口停站:跳转到 MAIN 首条语句,让用户聚焦起点(不打断后续停站定位)
      if (entryMode && get().currentLine > 0) get().reveal('debug', get().currentLine)
    } catch {
      // 无源码(如 com 公共库只有 42m):明确置空,避免在旧文件上标错停站行
      if (entryMode) {
        // 客制母版回退:cpm_apmt580_wf.4gl(首字母 a→c 的客制目录命名)
        try {
          const { source } = await api.sourceByFile(sessionId, `c${module}_${runProg || prog}.4gl`, module)
          set({ sourceContent: source.content, sourcePath: source.path, sourceDVM: f })
          if (get().currentLine === 0) {
            jumpToMain(set, get)
            get().pushTimeline({ origin: 'system', kind: 'info', text: '入口停站:已显示客制源码,点击行号下断点后点「继续 F5」开始' })
          }
          if (get().currentLine > 0) get().reveal('debug', get().currentLine)
          return
        } catch { /* 客制也没有 */ }
      }
      set({ sourceContent: '', sourcePath: '', sourceDVM: f })
      if (line) set({ currentLine: line })
    } finally {
      set({ loadingSource: false })
    }
  },

  refreshFrames: async () => {
    const { sessionId, state } = get()
    if (!sessionId || state !== 'stopped') return []
    try {
      const { frames } = await api.where(sessionId)
      set({ frames })
      return frames
    } catch { set({ frames: [] }); return [] }
  },

  refreshWatches: async () => {
    const { sessionId, watches, state } = get()
    if (!sessionId || state !== 'stopped') return
    const out: Watch[] = []
    for (const w of watches) {
      try {
        const { value } = await api.print(sessionId, w.expr)
        out.push({ expr: w.expr, value })
      } catch (e: any) {
        out.push({ expr: w.expr, error: e.message || String(e) })
      }
    }
    set({ watches: out })
  },

  addWatch: async (expr) => {
    const st = get()
    if (!expr.trim() || st.watches.some((w) => w.expr === expr)) return
    set({ watches: [...st.watches, { expr }] })
    await st.refreshWatches()
    st.pushTimeline({ origin: 'human', kind: 'command', text: `监视 ${expr}` })
  },

  removeWatch: (expr) => set((st) => ({ watches: st.watches.filter((w) => w.expr !== expr) })),

  control: async (action, arg) => {
    const { sessionId } = get()
    if (!sessionId) return
    get().pushTimeline({ origin: 'human', kind: 'command', text: arg ? `${action} ${arg}` : action })
    try {
      const resp = await api.control(sessionId, action, arg)
      if (action !== 'interrupt') {
        await get().refreshSnapshot()
        const st = get()
        // 步进可能跨模块:停站文件与当前显示源码不同时,跟随切换(源码到位才落光标)
        if (st.stop?.file && st.stop.file !== st.sourceDVM) void st.refreshSource(st.stop.file, resp?.stop?.line)
        // 停站后按面板开关同步调用栈与监视取值
        if (st.state === 'stopped') {
          if (get().stackAuto) void st.refreshFrames()
          void st.refreshWatches()
        }
      }
      if (action === 'run' || action === 'continue') pollUntilStopped(set, get)
      if (resp?.stop && (action === 'next' || action === 'step' || action === 'finish' || action === 'until')) {
        const nl = !!resp.stop.file && resp.stop.file !== get().sourceDVM
        set({ selectedFrame: -1, loadingSource: nl, activeTab: 'debug' })
        if (nl) void get().refreshSource(resp.stop.file, resp.stop.line) // 完成→reveal
        else get().reveal('debug', resp.stop.line ?? 0)
      }
    } catch (e: any) {
      get().pushTimeline({ origin: 'system', kind: 'warn', text: `${action} 失败: ${e.message}` })
    }
  },

  toggleBreakpoint: async (line) => {
    const st = get()
    if (!st.sessionId) return
    // 当前视图仅显示一个文件的源码:按行号匹配;另查"点击行→注册断点"映射
    // (fgldb 会把空行/注释行上的断点自动调整到下一条可执行语句,点原行也要能取消)
    // 浏览页:断点归属页签文件(location 用 文件:行);调试页沿用现有归属逻辑
    const tabFile = st.activeTab !== 'debug' ? st.tabs.find((t) => t.key === st.activeTab)?.file : ''
    const inTab = (f?: string) => !!f && !!tabFile && f.replace(/^.*[\/]/, '').toLowerCase() === tabFile.toLowerCase()
    const existing = tabFile
      ? st.breakpoints.find((b) => inTab(b.file) && b.line === line)
      : st.breakpoints.find((b) => b.line === line) ||
        (st.adjustedBps[line] !== undefined
          ? st.breakpoints.find((b) => b.num === st.adjustedBps[line])
          : undefined)
    try {
      let newBp: Breakpoint | undefined
      if (existing) {
        await api.bpDel(st.sessionId, existing.num)
        st.pushTimeline({ origin: 'human', kind: 'command', text: `删除断点 ${existing.file}:${existing.line}` })
      } else {
        const loc = tabFile ? `${tabFile}:${line}` : st.stop?.file ? `${st.stop.file}:${line}` : String(line)
        const { breakpoint } = await api.bpAdd(st.sessionId, loc)
        newBp = breakpoint
        st.pushTimeline({ origin: 'human', kind: 'command', text: `断点 ${breakpoint.file}:${breakpoint.line}` })
      }
      await st.refreshSnapshot()
      // 维护"点击行→注册断点"映射:实际注册行号与点击行不同时记录;失效映射清理
      const next: Record<number, number> = {}
      for (const [clickLine, num] of Object.entries(st.adjustedBps)) {
        if (get().breakpoints.some((b) => b.num === num)) next[Number(clickLine)] = num
      }
      if (newBp && newBp.line !== line) next[line] = newBp.num
      set({ adjustedBps: next })
    } catch (e: any) {
      st.pushTimeline({ origin: 'system', kind: 'warn', text: `断点操作失败: ${e.message}` })
    }
  },

  removeBreakpoint: async (num) => {
    const st = get()
    if (!st.sessionId) return
    try {
      await api.bpDel(st.sessionId, num)
      await st.refreshSnapshot()
    } catch (e: any) {
      st.pushTimeline({ origin: 'system', kind: 'warn', text: `删除断点失败: ${e.message}` })
    }
  },

  // 选择栈帧:切换 print/locals 求值上下文并跳转该帧源码位置(仅停站时可用)
  selectFrame: async (idx) => {
    const st = get()
    if (!st.sessionId || st.state !== 'stopped') return
    const frame = st.frames.find((f) => f.idx === idx)
    try {
      await api.frame(st.sessionId, idx)
      set({ selectedFrame: idx })
      if (frame) {
        st.pushTimeline({ origin: 'human', kind: 'command', text: `选帧 #${idx} ${frame.func} ${frame.file}:${frame.line}` })
        if (frame.file && frame.file !== st.sourceDVM) await st.refreshSource(frame.file, frame.line)
        else if (frame.line) get().reveal('debug', frame.line)
      }
      void st.refreshWatches()
    } catch (e: any) {
      st.pushTimeline({ origin: 'system', kind: 'warn', text: `选帧失败: ${e.message}` })
    }
  },

  toggleBPEnabled: async (num, enabled) => {
    const st = get()
    if (!st.sessionId) return
    try {
      await api.bpEnabled(st.sessionId, num, enabled)
      st.pushTimeline({ origin: 'human', kind: 'command', text: `${enabled ? '启用' : '禁用'}断点 #${num}` })
      await st.refreshSnapshot()
    } catch (e: any) {
      st.pushTimeline({ origin: 'system', kind: 'warn', text: `断点启停失败: ${e.message}` })
    }
  },

  // 断点列表点击跳转:必要时切换到断点所在文件,再定位到断点行(不改变运行上下文)
  jumpToBp: async (b) => {
    const st = get()
    if (b.file && b.file !== st.sourceDVM) await st.refreshSource(b.file)
    get().reveal('debug', b.line)
  },

  // 运行到光标:当前文件即停站文件时用行号,否则带文件名(fgldb until [file:]line)
  runToCursor: async (line) => {
    const st = get()
    if (!st.sessionId || st.state !== 'stopped') return
    const cur = st.sourceDVM
    const top = st.stop?.file || st.frames[0]?.file || ''
    const arg = cur && top && cur !== top ? `${cur}:${line}` : String(line)
    await st.control('until', arg)
  },

  quit: async () => {
    const st = get()
    if (!st.sessionId) return
    let kept = true
    try {
      const r = await api.quit(st.sessionId)
      kept = r?.kept !== false
      st.pushTimeline({ origin: 'human', kind: 'command', text: '结束调试(会话保留,可直接再次启动)' })
    } catch { /* ignore */ }
    if (snapTimer) { clearInterval(snapTimer); snapTimer = undefined }
    stopHoldTimer()
    // 结束本轮调试:运行现场清空,会话与源码保留(idle)。仅在后端整体断开时才清绑定
    const base = {
      started: false, stop: null, breakpoints: [], frames: [], adjustedBps: {},
      currentLine: 0, autovars: [], selectedFrame: -1,
    }
    if (kept) {
      set({ state: 'idle', ...base })
    } else {
      set({
        sessionId: null, module: '', prog: '', runProg: '', sessionEnv: '', state: '', ...base,
        backendDead: '', tabs: [], activeTab: 'debug', sourceContent: '', sourcePath: '', sourceDVM: '', lineOffset: 0,
      })
    }
  },

  restart: async () => {
    const st = get()
    if (!st.sessionId) return
    const { module, prog, lastReplayRowid } = st
    st.pushTimeline({ origin: 'human', kind: 'command', text: `重新开始 ${module}/${prog}${lastReplayRowid ? '(按原接口日志重放)' : ''}` })
    try { await api.quit(st.sessionId) } catch { /* 忽略,直接重启 */ }
    if (snapTimer) { clearInterval(snapTimer); snapTimer = undefined }
    stopHoldTimer()
    // 结束本轮(会话保留 idle),再启动新的一轮——宿主复用,免重新登录。
    // 重放会话必须按原日志重放(报文参数在服务端 ArgsOverride 里,普通启动会丢参数)
    set({ state: 'idle', started: false, stop: null, breakpoints: [], frames: [], adjustedBps: {}, autovars: [], selectedFrame: -1, currentLine: 0 })
    if (lastReplayRowid) await get().replayStart(lastReplayRowid)
    else await get().launch(module, prog)
  },

  doPrint: async (expr) => {
    const st = get()
    if (!st.sessionId || !expr.trim()) return
    try {
      const { value } = await api.print(st.sessionId, expr)
      st.pushTimeline({ origin: 'human', kind: 'info', text: `print ${expr} → ${value}` })
      await st.addWatch(expr)
    } catch (e: any) {
      st.pushTimeline({ origin: 'system', kind: 'warn', text: `print ${expr} → ${e.message}` })
    }
  },

  adoptExisting: async () => {
    try {
      const { sessions } = await api.list()
      if (!sessions.length) return
      const s = sessions[0]
      set({ sessionId: s.id, module: s.module, prog: s.prog, runProg: s.runProg || s.prog, state: s.state, sessionEnv: s.env || '' })
      if (s.state === 'idle') {
        // 空闲宿主(idle):仅接管绑定,不刷源码;等下一次启动直接复用
        get().pushTimeline({ origin: 'system', kind: 'info', text: `会话空闲(环境 ${s.env || '默认'}),可直接启动调试` })
        return
      }
      get().pushTimeline({ origin: 'system', kind: 'info', text: `接管已存在的会话 ${s.module}/${s.prog}` })
      const snap = await api.snapshot(s.id)
      set({
        stop: snap.stop, breakpoints: snap.breakpoints || [],
        started: !!snap.started,
        currentLine: snap.stop?.line ?? 0, holdingSeconds: snap.holdingSeconds || 0,
      })
      if (snap.state === 'stopped') await get().refreshSource(snap.stop?.file)
      if (get().stackAuto) void get().refreshFrames()
      void get().refreshWatches()
      alignAutoPrefs(set, get) // 重连接管:自动变量开关对齐 + 补拉最近一次求值结果
    } catch { /* ignore */ }
  },

  // 会话管理:右侧「会话」sheet 的 结束/重启/切换(后端会先结束当前 debug)
  sessionOp: async (op, env) => {
    const st = get()
    const label = op === 'close' ? '结束会话' : op === 'restart' ? '重启会话' : '切换会话'
    try {
      if (op === 'close' && st.sessionId) await api.sessionClose(st.sessionId)
      else if (op === 'restart' && st.sessionId) await api.sessionRestart(st.sessionId)
      else if (op === 'switch' && env) await api.sessionSwitch(env)
      st.pushTimeline({ origin: 'human', kind: 'command', text: label + (env ? ' → ' + env : '') })
    } catch (e: any) {
      st.pushTimeline({ origin: 'system', kind: 'warn', text: `${label}失败: ${e.message || String(e)}` })
      throw e
    }
    await get().syncFromSessions()
  },

  // 会话被替换/关闭后,按服务端现状对齐本地绑定(源码保留便于浏览)
  syncFromSessions: async () => {
    if (snapTimer) { clearInterval(snapTimer); snapTimer = undefined }
    stopHoldTimer()
    const clearRun = {
      started: false, stop: null, currentLine: 0, breakpoints: [], adjustedBps: {},
      frames: [], autovars: [], selectedFrame: -1, holdingSeconds: 0,
    }
    try {
      const { sessions } = await api.list()
      const s0 = sessions[0]
      if (!s0) {
        // 会话全部断开
        set({ sessionId: null, module: '', prog: '', runProg: '', sessionEnv: '', state: '', ...clearRun, loadingSource: false })
        return
      }
      set({
        sessionId: s0.id, module: s0.module, prog: s0.prog, runProg: s0.runProg || s0.prog,
        sessionEnv: s0.env || '', state: s0.state,
      })
      if (s0.state === 'idle' || s0.state === 'loading') {
        set({ ...clearRun, loadingSource: false })
        return
      }
      if (s0.state === 'stopped') {
        const snap = await api.snapshot(s0.id)
        set({ stop: snap.stop, breakpoints: snap.breakpoints || [], started: !!snap.started, holdingSeconds: snap.holdingSeconds || 0 })
        if (snap.stop?.file) void get().refreshSource(snap.stop.file, snap.stop.line)
        if (get().stackAuto) void get().refreshFrames()
        void get().refreshWatches()
        alignAutoPrefs(set, get) // 会话切换/重连后把自动变量开关对齐到新会话
      }
    } catch { /* list/snapshot 失败:保持现状 */ }
  },
}))

function pollUntilStopped(set: (p: Partial<Store>) => void, get: () => Store) {
  if (snapTimer) clearInterval(snapTimer)
  snapTimer = window.setInterval(async () => {
    const st = get()
    if (!st.sessionId) { if (snapTimer) clearInterval(snapTimer); return }
    try {
      const snap = await api.snapshot(st.sessionId)
      // 跨文件停站:光标等 refreshSource 落位,不先画到旧文件上
      const nl = !!snap.stop?.file && snap.stop.file !== st.sourceDVM
      set({
        state: snap.state, stop: snap.stop, breakpoints: snap.breakpoints || [],
        started: !!snap.started,
        holdingSeconds: snap.holdingSeconds || 0,
        sessionEnv: snap.env || get().sessionEnv,
        module: snap.module || get().module,
        runProg: snap.runProg || get().runProg,
        currentLine: nl ? 0 : (snap.stop?.line || get().currentLine),
        loadingSource: nl ? true : get().loadingSource,
      })
      if (nl && snap.stop?.file) void get().refreshSource(snap.stop.file, snap.stop.line)
      else if (snap.state === 'stopped' && snap.stop?.line && !nl) get().reveal('debug', snap.stop.line)
      if (snap.state === 'stopped') {
        clearInterval(snapTimer!)
        snapTimer = undefined
        set({ activeTab: 'debug' })
        startHoldTimer(set, get)
        if (get().stackAuto) void get().refreshFrames()
        void get().refreshWatches()
        void get().refreshSource(snap.stop?.file)
        // 兜底:停站宣告与后端断点恢复收尾之间仍有微小窗口(事件先于快照到达),
        // 稍后补一次快照,保证缓存的断点无需步进就能显示
        window.setTimeout(() => {
          const cur = get()
          if (cur.sessionId && cur.state === 'stopped') void cur.refreshSnapshot()
        }, 2500)
        if (snap.stop?.reason === 'breakpoint') {
          st.pushTimeline({ origin: 'system', kind: 'stop', text: `命中断点 ${snap.stop.file}:${snap.stop.line}` })
        }
      }
      if (snap.state === 'exit') { clearInterval(snapTimer!); snapTimer = undefined }
      if (snap.state === 'idle') {
        // 本轮运行直接结束(程序立即退出等):回空闲,清运行现场(会话保留)
        clearInterval(snapTimer!); snapTimer = undefined
        stopHoldTimer()
        set({ started: false, stop: null, currentLine: 0, loadingSource: false, selectedFrame: -1, autovars: [], breakpoints: [], frames: [], adjustedBps: {} })
      }
    } catch {
      // 快照失败可能因为会话已被移除(启动失败/异常退出且 WS 事件未送达):
      // 确认会话确实消失后退出加载态并报错,避免永久转圈;瞬时网络错误则继续轮询
      const st = get()
      if (!st.sessionId) { if (snapTimer) { clearInterval(snapTimer); snapTimer = undefined } return }
      try {
        const { sessions } = await api.list()
        if (!sessions.some((x) => x.id === st.sessionId)) {
          if (snapTimer) { clearInterval(snapTimer); snapTimer = undefined }
          stopHoldTimer()
          set({ sessionId: null, state: '', loadingSource: false, stop: null,
            launchError: st.launchError || '调试会话启动失败(会话已消失),请检查作业名或服务器状态' })
        }
      } catch { /* list 也失败:保持轮询,等服务恢复 */ }
    }
  }, 1000)
}

// 页面「自动变量」开关与服务端会话对齐(求值在服务端做;启动/接管/换会话后重新下发),
// 已停站且开关开时拉一次最近求值结果,补上重连期间可能错过的 autovars 事件
function alignAutoPrefs(set: (p: Partial<Store>) => void, get: () => Store) {
  const sid = get().sessionId
  if (!sid || !get().autovarsAuto) return
  void api.autovarsAuto(sid, true).catch(() => { /* 会话瞬态:下次对齐逻辑重发 */ })
  if (get().state === 'stopped') {
    void api.autovars(sid).then(({ vars }) => set({ autovars: vars || [] })).catch(() => {})
  }
}

function startHoldTimer(set: (p: Partial<Store>) => void, get: () => Store) {
  stopHoldTimer()
  holdTimer = window.setInterval(() => {
    const st = get()
    if (st.state !== 'stopped') { stopHoldTimer(); return }
    set({ holdingSeconds: st.holdingSeconds + 1 })
  }, 1000)
}

function stopHoldTimer() {
  if (holdTimer) { clearInterval(holdTimer); holdTimer = undefined }
}

// 连接 WebSocket(自动重连)
export function connectWS() {
  const setWs = useStore.getState().setWsConnected
  const onEvent = useStore.getState().onEvent
  const pushRaw = useStore.getState().pushRaw
  let closed = false
  let ws: WebSocket | undefined

  function connect() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    ws = new WebSocket(`${proto}://${location.host}/api/ws`)
    ws.onopen = () => {
      setWs(true)
      // 连接/重连后按服务端实况对齐会话绑定:后端重启、会话被移走或页面长时间
      // 挂着时清掉残留引用,避免界面停在"会话进行中"锁死 wslogs 重放等操作
      void useStore.getState().syncFromSessions()
    }
    ws.onclose = () => {
      setWs(false)
      if (!closed) setTimeout(connect, 2000)
    }
    ws.onmessage = (m) => {
      try {
        const ev: Event = JSON.parse(m.data)
        onEvent(ev)
      } catch { pushRaw(String(m.data)) }
    }
  }
  connect()
  return () => { closed = true; ws?.close() }
}

// 调试句柄:浏览器控制台可用 __store.getState() 检查状态
if (typeof window !== 'undefined') {
  ;(window as unknown as Record<string, unknown>).__store = useStore
}

// 浏览器控制台调试入口(生产无副作用)
;(window as any).__store = useStore
