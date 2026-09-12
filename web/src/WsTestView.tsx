// 服务测试视图:复刻 awsq990「集成服务测试」——接口方式/网址/请求报文,直接执行看响应,
// 并用一张常驻表格记录每次执行(起始时间/HTTP code/运行结果/处理时间(秒),与原生 g_wsfa2_d 同列)。
// 后端经 SSH 在服务器上以 curl 调用(与 awsq990 同网络位置)。
import { useEffect, useState } from 'react'
import { Play } from 'lucide-react'
import { useStore } from './store'
import { api } from './api'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Checkbox, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui'

// 接口方式选项(wsfc001 映射,与 awsq990 一致)
const MODES: [string, string, string][] = [
  ['1', 'awsp900', 'Web service (SOAP)'],
  ['2', 'awsp900', 'Web service (SOAP) 备选'],
  ['3', 'awsp920', 'RESTful'],
  ['4', 'awsp940', 'OpenApi restful'],
  ['5', 'awsp930', 'OpenApi Web service'],
]

export function WsTestView() {
  const mode = useStore((s) => s.wsTestMode)
  const url = useStore((s) => s.wsTestUrl)
  const body = useStore((s) => s.wsTestBody)
  const soap = useStore((s) => s.wsTestSoap)
  const result = useStore((s) => s.wsTestResult)
  const running = useStore((s) => s.wsTestRunning)
  const err = useStore((s) => s.wsTestErr)
  const history = useStore((s) => s.wsTestHistory)
  const setWsTest = useStore((s) => s.setWsTest)
  const runWsTest = useStore((s) => s.runWsTest)
  const wsLogSel = useStore((s) => s.wsLogSel)
  const wsLogContent = useStore((s) => s.wsLogContent)
  // 默认地址里的区域别名(36→t35prd):取自 /api/status,避免占位符写死某一个区
  const [zoneName, setZoneName] = useState('')
  useEffect(() => {
    void api.status().then((s: any) => setZoneName(s.zoneName || '')).catch(() => {})
  }, [])

  const isSoap = mode === '1' || mode === '2' || mode === '5'
  const ep = MODES.find((m) => m[0] === mode)?.[1] || 'awsp920'
  const defaultUrl = `http://127.0.0.1/w${zoneName || 't35prd'}/ws/r/${ep}`

  const doRun = () => void runWsTest()

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* 工具条:下缘分割线与报文区连成整体 */}
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-2 py-1.5">
        {/* <FlaskConical className="h-4 w-4 text-muted-foreground" /> */}
        <Select value={mode} onValueChange={(v) => setWsTest({ mode: v, url: '' })}>
          <SelectTrigger className="h-7 w-[240px] text-xs" title="接口方式(wsfc001 映射)">
            <SelectValue placeholder="接口方式" />
          </SelectTrigger>
          <SelectContent>
            {MODES.map(([v, ep2, label]) => (
              <SelectItem key={v} value={v} className="text-xs">{label} ({ep2})</SelectItem>
            ))}
          </SelectContent>
        </Select>
        <input
          value={url}
          onChange={(e) => setWsTest({ url: e.target.value })}
          placeholder={defaultUrl}
          className="h-7 min-w-0 flex-1 border border-border bg-background px-2 font-mono text-xs text-foreground placeholder:text-muted-foreground focus:border-border focus:outline-none"
          title={defaultUrl}
        />
        {isSoap && (
          <div className="flex items-center gap-1.5 text-xs text-muted-foreground" title="SOAP 报文(SOAPAction 空头)">
            <Checkbox id="wstest-soap" checked={soap} onCheckedChange={(v) => setWsTest({ soap: v === true })} />
            <label htmlFor="wstest-soap" className="cursor-pointer select-none">SOAP</label>
          </div>
        )}
        <button onClick={doRun} disabled={running || !body.trim()}
          title="执行接口调用(服务器侧 curl POST)"
          className="inline-flex items-center gap-1 border border-emerald-500/20 px-2.5 py-1 text-xs text-emerald-600 dark:text-emerald-400 hover:bg-emerald-500/10 disabled:opacity-40">
          <Play className="h-3.5 w-3.5" fill="currentColor" />
          {running ? '执行中…' : '执行'}
        </button>
        {wsLogSel && (
          <button
            onClick={() => setWsTest({ body: wsLogContent?.request || wsLogSel.reqPath, soap: false })}
            title={`带入日志报文:${wsLogSel.service}`}
            className="border border-border px-2 py-1 text-xs text-muted-foreground hover:bg-accent"
          >
            从日志带入({wsLogSel.service.length > 16 ? wsLogSel.service.slice(0, 16) + '…' : wsLogSel.service})
          </button>
        )}
      </div>

      {err && <div className="shrink-0 border border-red-500/20 bg-red-500/10 px-3 py-1.5 text-xs text-red-600 dark:text-red-600 dark:text-red-400">{err}</div>}

      {/* 测试日志表(复刻 awsq990 集成服务测试页的常驻表格):
          列 = 起始时间 / HTTP code / 运行结果 / 处理时间(秒);最新在前。
          刻意不做排序 —— 这是时序日志;目标地址放在行 tooltip 里。
          点击行:把该次的请求报文与响应一起填回下方两个 pane(原生只展示,这里是超集)。 */}
      <div className="max-h-44 shrink-0 overflow-auto border-b border-border bg-background">
        <Table container={false} className="table-fixed text-xs">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="sticky top-0 z-10 w-40 border-b border-border bg-background">起始时间</TableHead>
              <TableHead className="sticky top-0 z-10 w-20 border-b border-border bg-background">HTTP code</TableHead>
              <TableHead className="sticky top-0 z-10 w-40 border-b border-border bg-background">运行结果</TableHead>
              <TableHead className="sticky top-0 z-10 w-28 border-b border-border bg-background">处理时间(秒)</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {history.length === 0 && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={4} className="border-0 px-2 py-2 text-center text-xs text-muted-foreground">
                  每次「执行」在此记录一行(与 T100 原生集成服务测试页一致)
                </TableCell>
              </TableRow>
            )}
            {history.map((h, i) => (
              <TableRow key={i} data-state={i === 0 ? 'selected' : undefined}
                onClick={() => setWsTest({
                  body: h.body,
                  result: { httpCode: h.httpCode, durationSec: h.durationSec, response: h.response },
                })}
                title={`点击回看该次请求/响应\n${h.url}`}
                className="h-7 cursor-pointer border-b border-border/60 hover:bg-accent/40">
                <TableCell className="border-0 px-2 py-0 font-mono text-[11px] text-muted-foreground">{h.at}</TableCell>
                <TableCell className={`border-0 px-2 py-0 font-mono ${h.httpCode === 200 ? 'text-emerald-600 dark:text-emerald-500' : h.httpCode > 0 ? 'text-red-500' : 'text-muted-foreground'}`}>
                  {h.httpCode > 0 ? h.httpCode : '-'}
                </TableCell>
                <TableCell className="border-0 px-2 py-0">
                  <span className="block truncate" title={h.result}>{h.result}</span>
                </TableCell>
                <TableCell className="border-0 px-2 py-0 font-mono text-[11px] text-muted-foreground">{h.durationSec.toFixed(3)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
        {/* 请求报文:竖排时下缘分割线,横排时右缘分割线(与响应区紧贴相连) */}
        <div className="flex min-h-40 flex-1 flex-col overflow-hidden border-b border-border bg-background lg:border-b-0 lg:border-r">
          <div className="flex h-8 shrink-0 items-center border-b border-border px-2.5 text-xs font-medium text-muted-foreground">
            请求报文(JSON / XML)
          </div>
          <textarea
            value={body}
            onChange={(e) => setWsTest({ body: e.target.value })}
            spellCheck={false}
            placeholder={'{\n  "key": "...",\n  "type": "sync",\n  "host": { "prod": "T100", ... },\n  "service": { "name": "xxx" },\n  "payload": { ... }\n}'}
            className="min-h-0 flex-1 resize-none bg-transparent p-2 font-mono text-xs leading-5 text-foreground placeholder:text-muted-foreground focus:outline-none"
          />
        </div>

        {/* 响应 */}
        <div className="flex min-h-40 flex-1 flex-col overflow-hidden bg-background">
          <div className="flex h-8 shrink-0 items-center gap-3 border-b border-border px-2.5 text-xs font-medium text-muted-foreground">
            响应
            {result && result.httpCode > 0 && (
              <>
                <span className={`font-mono ${result.httpCode === 200 ? 'text-emerald-600 dark:text-emerald-500' : 'text-red-500'}`}>
                  HTTP {result.httpCode}
                </span>
                <span className="font-mono text-muted-foreground">{result.durationSec.toFixed(3)}s</span>
              </>
            )}
            {result?.error && <span className="text-red-600 dark:text-red-400">{result.error}</span>}
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            {result?.response ? (
              <pre className="whitespace-pre-wrap break-all p-2 font-mono text-[11px] leading-5 text-foreground">{result.response}</pre>
            ) : (
              <div className="p-3 text-xs text-muted-foreground">{running ? '请求中…' : '执行后在 此显示响应报文'}</div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
