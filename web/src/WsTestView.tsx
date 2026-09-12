// 服务测试视图:复刻 awsq990「集成服务测试」——接口方式/网址/请求报文,直接执行看响应,
// 并用一张常驻表格记录每次执行(起始时间/HTTP code/运行结果/处理时间(秒),与原生 g_wsfa2_d 同列)。
// 后端经 SSH 在服务器上以 curl 调用(与 awsq990 同网络位置)。
import { useEffect, useState } from 'react'
import { Play } from 'lucide-react'
import { useStore } from './store'
import { api } from './api'
import { PayloadEditor } from './PayloadEditor'
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
      {/* 工具条分两行(与日志页同样式):第一行 接口方式 + 集成方式(地址);
          第二行 SOAP 勾选 + 执行 + 从日志带入。字段不再拉满整行,留白更清楚。 */}
      <div className="shrink-0 border-b border-border">
        <div className="flex flex-wrap items-center gap-2 px-2 pt-2 pb-1.5">
          <Select value={mode} onValueChange={(v) => setWsTest({ mode: v, url: '' })}>
            <SelectTrigger className="h-7 w-[200px] text-xs" title="接口方式(wsfc001 映射)">
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
            className="h-7 w-[440px] min-w-0 border border-border bg-background px-2 font-mono text-xs text-foreground placeholder:text-muted-foreground focus:border-border focus:outline-none"
            title={defaultUrl}
          />
        </div>
        <div className="flex flex-wrap items-center gap-2 px-2 pt-1.5 pb-2">
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
      </div>

      {err && <div className="shrink-0 border border-red-500/20 bg-red-500/10 px-3 py-1.5 text-xs text-red-600 dark:text-red-600 dark:text-red-400">{err}</div>}
      <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
        {/* 请求报文:竖排时下缘分割线,横排时右缘分割线(与响应区紧贴相连)。
            背景交给 Monaco 主题(editor.background,与源码编辑器同色),故这里不再铺 bg。 */}
        <div className="flex min-h-40 flex-1 flex-col overflow-hidden border-b border-border lg:border-b-0 lg:border-r">
          <div className="flex h-8 shrink-0 items-center border-b border-border bg-card px-2.5 text-xs font-medium text-muted-foreground">
            请求报文(JSON / XML)
          </div>
          <div className="min-h-0 flex-1">
            <PayloadEditor text={body} editable onChange={(v) => setWsTest({ body: v })} path="wstest:request" />
          </div>
        </div>

        {/* 响应 */}
        <div className="flex min-h-40 flex-1 flex-col overflow-hidden">
          <div className="flex h-8 shrink-0 items-center gap-3 border-b border-border bg-card px-2.5 text-xs font-medium text-muted-foreground">
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
          <div className="min-h-0 flex-1">
            <PayloadEditor text={result?.response} loading={running} path="wstest:response"
              emptyHint="执行后在此显示响应报文" />
          </div>
        </div>
      </div>
    </div>
  )
}
