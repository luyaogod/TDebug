// REST API 客户端
export interface Frame { idx: number; func: string; file: string; line: number }
export interface SourceLine { num: number; text: string; isCur: boolean }
export interface StopInfo {
  reason: string; bpNum?: number; func?: string; file?: string; line?: number
  frames?: Frame[]; source?: SourceLine[]
}
export interface Breakpoint { num: number; file: string; line: number; func?: string; enabled: boolean; note?: string }
export interface VarItem { expr: string; value?: string }
export interface VarDecl { name: string; type: string }
// 正在执行的那条命令 —— 界面上的「AI 正在执行 continue(已 12s)」
export interface InflightInfo { cmd: string; since: string; elapsed: number }
export interface SessionBrief {
  id: string; module: string; prog: string; runProg?: string; state: string
  env?: string
  file?: string; line?: number; func?: string; reason?: string
  holdingSeconds: number; breakpoints: number; watchdogSeconds: number
  // 谁在驾驶:solo(纯人工)| collab(协作,AI 主导)
  mode?: string
  inflight?: InflightInfo
  // 停站行本身就是交互语句(程序把控制权交给了界面)
  waitingForUser?: boolean; waitingKind?: string
  // 运行态下距最近一次协议输出的秒数
  silentSeconds?: number
}
export interface WSLogItem {
  rowid: string; service: string; pid: string
  start: string; end: string; duration: string
  code: string; job: string
  reqPath: string; rspPath: string; reqSize: string; rspSize: string; errMsg: string
  // 对齐 T100 原生页 awsq990 补上的三项(中文名以 tdict rt wsfa_t 字典为准)
  origin: string  // wsfa013 发起端
  server: string  // wsfa018 服务端
  sso: string     // wsfa015 sso秒数
}
export interface WSLogContent {
  request: string; response: string
  // 请求报文内容不完整(超读取上限被截断 / 源文件已清理只剩前 2000 字符):
  // 按原报文重放不受影响,但不能基于它编辑后重放(会送出残缺报文)
  requestPartial?: boolean
}
// 接口日志查询条件(对齐 T100 原生页 awsq990 的 QBE):
// startFrom = wsfa003(起始时间)下界,endTo = wsfa004(结束时间)上界;
// result/origin/server 为等值过滤(wsfa006/wsfa013/wsfa018)
export interface WSLogQuery {
  service?: string
  job?: string // wsfa012 作业编号(支持 * ? 通配)—— 服务名是反域名,按作业找日志靠这个
  result?: string
  origin?: string
  server?: string
  pid?: string // wsfa002 服务程序序号(界面「服务程序」列;与 service 一起可精确定位某一次调用)
  onlyFail?: boolean
  page?: number
  startFrom?: string
  endTo?: string
}

export interface WSTestResult {
  httpCode: number; durationSec: number; response: string; error?: string
}

export interface Event {
  type: string; sessionId: string; time: string
  state?: string; stop?: StopInfo; vars?: VarItem[]; text?: string
  // 单调递增序号(服务端分配):WS 开场补发与去重用
  seq?: number
  // 谁发起的:ai | human | system
  actor?: string
  // 机器可读的动作名(bp.add / control.step / session.mode …),text 是给人看的
  action?: string
}

// WS 建连时的开场补发帧:只追加时间线,不触发任何副作用
export interface ReplayFrame { type: 'replay'; epoch: string; events: Event[] }

// 浏览器不声明 X-Actor —— 服务端不认这个头时一律按 human 处理(CLI 才声明 ai)。
// 注意 headers 必须在 opts 之后合并:早先写成 { headers: 默认, ...opts } 时,
// 调用方只要自带 headers 就会把默认头整块覆盖掉。
async function req<T>(url: string, opts?: RequestInit): Promise<T> {
  const r = await fetch(url, {
    ...opts,
    headers: { 'Content-Type': 'application/json', ...(opts?.headers || {}) },
  })
  const data = await r.json().catch(() => ({}))
  if (!r.ok || data.ok === false) throw new Error(data.error || `HTTP ${r.status}`)
  return data as T
}

export const api = {
  status: () => req<any>('/api/status'),
  settings: () => req<any>('/api/settings'),
  saveSettings: (cfg: any) => req<any>('/api/settings', { method: 'PUT', body: JSON.stringify(cfg) }),
  // 自动获取数据库连接要素(SSH 上服务器探测「从服务器获取」;note 说明未获取到的原因)
  probeDB: (body: { host: string; port: number; user: string; password: string; zone: string; type: string }) =>
    req<{ type: string; tns?: string; port?: number; database?: string; sqlplus?: string; oracleHome?: string; twoTask?: string; host?: string; service?: string; note?: string }>('/api/dbprobe', { method: 'POST', body: JSON.stringify(body) }),
  // 客户端直连测试(按表单显式字段连库,凭据取账号列表首项)
  connTest: (body: { type: string; host: string; port: number; service?: string; database?: string; accounts?: { account: string; password: string }[] }) =>
    req<{ ok: boolean; version?: string; error?: string }>('/api/conntest', { method: 'POST', body: JSON.stringify(body) }),
  // 账号清单「验证」:SSH 上服务器以该账号+密码连显式目标库 select 1(只读)
  dbAccVerify: (body: { host: string; port: number; user: string; password: string; zone: string; type: string; account: string; acctPassword: string; dbHost?: string; dbPort?: number; dbSvc?: string; dbDatabase?: string }) =>
    req<{ ok: boolean; error?: string }>('/api/dbaccverify', { method: 'POST', body: JSON.stringify(body) }),
  list: () => req<{ sessions: SessionBrief[] }>('/api/sessions'),
  launch: (module: string, prog: string, opts?: { ssh?: string; zone?: string }) =>
    req<{ sessionId: string; module?: string; prog?: string; runProg?: string }>('/api/sessions', { method: 'POST', body: JSON.stringify({ module, prog, ...opts }) }),
  snapshot: (id: string) => req<any>(`/api/sessions/${id}`),
  // 结束调试:只结束本轮运行,宿主会话保留(idle),可直接再次启动
  quit: (id: string) => req<any>(`/api/sessions/${id}`, { method: 'DELETE' }),
  // 会话管理(单一常驻会话)
  sessionRestart: (id: string) => req<{ sessionId: string; env?: string; state?: string }>(`/api/sessions/${id}/restart`, { method: 'POST' }),
  sessionClose: (id: string) => req<any>(`/api/sessions/${id}/close`, { method: 'POST' }),
  sessionSwitch: (env: string) => req<{ sessionId: string; env?: string; state?: string }>('/api/sessions/switch', { method: 'POST', body: JSON.stringify({ env }) }),
  // 空闲态重新设置会话 TOPENT(空值 = 清除覆盖并重新下发配置默认)
  topent: (id: string, value: string) => req<{ topent: string }>(`/api/sessions/${id}/topent`, { method: 'POST', body: JSON.stringify({ value }) }),
  bpAdd: (id: string, location: string) =>
    req<{ breakpoint: Breakpoint }>(`/api/sessions/${id}/breakpoints`, { method: 'POST', body: JSON.stringify({ location }) }),
  bpDel: (id: string, num: number) => req<any>(`/api/sessions/${id}/breakpoints/${num}`, { method: 'DELETE' }),
  control: (id: string, action: string, arg?: string) =>
    req<any>(`/api/sessions/${id}/control`, { method: 'POST', body: JSON.stringify({ action, arg }) }),
  // 切模式(纯人工/协作)。人可任意方向切;AI 不能自行解除纯人工模式(服务端 403)
  setMode: (id: string, mode: 'solo' | 'collab') =>
    req<{ mode: string }>(`/api/sessions/${id}/mode`, { method: 'POST', body: JSON.stringify({ mode }) }),
  print: (id: string, expr: string) => req<{ value: string }>(`/api/sessions/${id}/print`, { method: 'POST', body: JSON.stringify({ expr }) }),
  where: (id: string) => req<{ frames: Frame[] }>(`/api/sessions/${id}/where`, { method: 'POST' }),
  raw: (id: string, command: string) => req<{ lines: string[] }>(`/api/sessions/${id}/raw`, { method: 'POST', body: JSON.stringify({ command }) }),
  locals: (id: string) => req<{ vars: VarItem[] }>(`/api/sessions/${id}/locals`),
  globals: (id: string, limit?: number) => req<{ vars: VarDecl[]; total: number }>(`/api/sessions/${id}/globals${limit ? `?limit=${limit}` : ''}`),
  sources: (id: string) => req<{ sources: string[] }>(`/api/sessions/${id}/sources`),
  functions: (id: string, limit?: number) => req<{ functions: string[]; total: number }>(`/api/sessions/${id}/functions${limit ? `?limit=${limit}` : ''}`),
  autovars: (id: string) => req<{ vars: VarItem[] }>(`/api/sessions/${id}/autovars`),
  // 自动变量面板开关:停站后是否由服务端自动求值当前源码窗变量(默认关)
  autovarsAuto: (id: string, auto: boolean) => req<any>(`/api/sessions/${id}/autovars`, { method: 'POST', body: JSON.stringify({ auto }) }),
  frame: (id: string, num: number) => req<{ frame: number }>(`/api/sessions/${id}/frame`, { method: 'POST', body: JSON.stringify({ num }) }),
  bpEnabled: (id: string, num: number, enabled: boolean) =>
    req<any>(`/api/sessions/${id}/breakpoints/${num}/enabled`, { method: 'POST', body: JSON.stringify({ enabled }) }),
  sourceByFile: (id: string, file: string, module: string) =>
    req<{ source: { path: string; content: string; dvmFile?: string } }>(`/api/sessions/${id}/source?file=${encodeURIComponent(file)}&module=${encodeURIComponent(module)}`),
  // 定位函数到源文件与行号(fgldb info line;仅停站可用)
  locate: (id: string, word: string) =>
    req<{ file: string; line: number }>(`/api/sessions/${id}/locate`, { method: 'POST', body: JSON.stringify({ word }) }),
  // 行号校准:检测 fgldb(DVM)行号与磁盘源码的偏移(仅停站可用)
  calibrate: (id: string) => req<{ offset: number }>(`/api/sessions/${id}/calibrate`, { method: 'POST' }),
  wsTest: (mode: string, url: string, body: string, soap: boolean) =>
    req<{ result: WSTestResult }>('/api/wstest', { method: 'POST', body: JSON.stringify({ mode, url, body, soap }) }),
  // 条件用对象传递(参数变多后位置参数太容易错位);空值不发送
  wsLogs: (q: WSLogQuery) => {
    const p = new URLSearchParams()
    for (const [k, v] of Object.entries(q)) {
      if (v === undefined || v === null || v === '' || v === false) continue
      p.set(k, typeof v === 'boolean' ? (v ? '1' : '0') : String(v))
    }
    p.set('onlyFail', q.onlyFail ? '1' : '0')
    p.set('page', String(q.page ?? 1))
    p.set('pageSize', '200')
    return req<{ items: WSLogItem[]; hasMore: boolean }>(`/api/wslogs?${p.toString()}`)
  },
  wsLogContent: (rowid: string) =>
    req<{ item: WSLogItem; content: WSLogContent }>(`/api/wslogs/content?rowid=${encodeURIComponent(rowid)}`),
  // 重放调试:request 非空 = 用界面里改过的入参(后端落临时文件后作为入参文件)
  wsLogDebug: (rowid: string, request?: string) =>
    req<{ sessionId: string; module?: string; prog?: string; runProg?: string }>('/api/wslogs/debug', {
      method: 'POST',
      body: JSON.stringify(request ? { rowid, request } : { rowid }),
    }),
  sourcePreview: (module: string, prog: string) =>
    req<{ source: { path: string; content: string; dvmFile?: string } }>(`/api/source-preview?module=${encodeURIComponent(module)}&prog=${encodeURIComponent(prog)}`),
}
