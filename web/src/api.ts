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
export interface SessionBrief {
  id: string; module: string; prog: string; runProg?: string; state: string
  env?: string
  file?: string; line?: number; func?: string; reason?: string
  holdingSeconds: number; breakpoints: number; watchdogSeconds: number
}
export interface WSLogItem {
  rowid: string; service: string; pid: string
  start: string; end: string; duration: string
  code: string; job: string
  reqPath: string; rspPath: string; reqSize: string; rspSize: string; errMsg: string
}
export interface WSLogContent {
  request: string; response: string
}

export interface WSTestResult {
  httpCode: number; durationSec: number; response: string; error?: string
}

export interface Event {
  type: string; sessionId: string; time: string
  state?: string; stop?: StopInfo; vars?: VarItem[]; text?: string
}

async function req<T>(url: string, opts?: RequestInit): Promise<T> {
  const r = await fetch(url, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
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
  // 空闲态重新设置会话 TOPENT(空值 = 清除,回配置/登录默认)
  topent: (id: string, value: string) => req<{ topent: string }>(`/api/sessions/${id}/topent`, { method: 'POST', body: JSON.stringify({ value }) }),
  bpAdd: (id: string, location: string) =>
    req<{ breakpoint: Breakpoint }>(`/api/sessions/${id}/breakpoints`, { method: 'POST', body: JSON.stringify({ location }) }),
  bpDel: (id: string, num: number) => req<any>(`/api/sessions/${id}/breakpoints/${num}`, { method: 'DELETE' }),
  control: (id: string, action: string, arg?: string) =>
    req<any>(`/api/sessions/${id}/control`, { method: 'POST', body: JSON.stringify({ action, arg }) }),
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
  wsLogs: (service: string, onlyFail: boolean, page: number, startFrom: string, startTo: string) =>
    req<{ items: WSLogItem[]; hasMore: boolean }>(`/api/wslogs?service=${encodeURIComponent(service)}&onlyFail=${onlyFail ? 1 : 0}&page=${page}&pageSize=200&startFrom=${encodeURIComponent(startFrom)}&startTo=${encodeURIComponent(startTo)}`),
  wsLogContent: (rowid: string) =>
    req<{ item: WSLogItem; content: WSLogContent }>(`/api/wslogs/content?rowid=${encodeURIComponent(rowid)}`),
  wsLogDebug: (rowid: string) =>
    req<{ sessionId: string; module?: string; prog?: string; runProg?: string }>('/api/wslogs/debug', { method: 'POST', body: JSON.stringify({ rowid }) }),
  sourcePreview: (module: string, prog: string) =>
    req<{ source: { path: string; content: string; dvmFile?: string } }>(`/api/source-preview?module=${encodeURIComponent(module)}&prog=${encodeURIComponent(prog)}`),
}
