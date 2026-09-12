// 服务测试视图:复刻 awsq990「集成服务测试」——接口方式/网址/请求报文,直接执行看响应。
// 后端经 SSH 在服务器上以 curl 调用(与 awsq990 同网络位置)。
import { useState } from 'react'
import { FlaskConical, Play } from 'lucide-react'
import { useStore } from './store'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Checkbox } from './ui'

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
  const [showHist, setShowHist] = useState(false)

  const isSoap = mode === '1' || mode === '2' || mode === '5'
  const ep = MODES.find((m) => m[0] === mode)?.[1] || 'awsp920'
  const defaultUrl = `http://127.0.0.1/wt35prd/ws/r/${ep}`

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
        {history.length > 0 && (
          <button onClick={() => setShowHist(!showHist)}
            className="border border-border px-2 py-1 text-xs text-muted-foreground hover:bg-accent">
            历史({history.length})
          </button>
        )}
      </div>

      {err && <div className="shrink-0 border border-red-500/20 bg-red-500/10 px-3 py-1.5 text-xs text-red-600 dark:text-red-600 dark:text-red-400">{err}</div>}

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

      {/* 执行历史 */}
      {showHist && history.length > 0 && (
        <div className="max-h-44 shrink-0 overflow-auto border-t border-border bg-background">
          {history.map((h, i) => (
            <div key={i} onClick={() => setWsTest({ result: { httpCode: h.httpCode, durationSec: h.durationSec, response: h.response } })}
              title="点击回看该次响应"
              className="flex h-7 cursor-pointer items-center gap-2 border-b border-border/60 px-2 text-xs last:border-0 hover:bg-accent/40">
              <span className="w-20 shrink-0 font-mono text-[11px] text-muted-foreground">{h.time}</span>
              <span className={`w-14 shrink-0 font-mono ${h.httpCode === 200 ? 'text-emerald-600 dark:text-emerald-500' : 'text-red-500'}`}>{h.httpCode}</span>
              <span className="w-20 shrink-0 font-mono text-[11px] text-muted-foreground">{h.durationSec.toFixed(3)}s</span>
              <span className="min-w-0 flex-1 truncate text-muted-foreground" title={h.url}>{h.url}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
