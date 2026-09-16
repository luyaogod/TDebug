// 桌面链路冒烟(无 GUI):验证 Electron 依赖的 Go 侧契约 ——
//   1) `tdebug desktop` 在空目录首启:自动建 config.json,打印 TDEBUG_READY{json},写状态文件;
//   2) /api/status 可达且端口与 READY 行一致;GET / 返回界面 HTML;
//   3) 同数据目录再起一个 → 打印 attached:true 并立即退出(壳接管既有实例);
//   4) POST /api/shutdown → 优雅退出,状态文件被清理。
// 用法:node desktop/scripts/smoke.mjs [--bin <tdebug.exe>]
// 退出码 0 = 全部通过;失败时打印日志尾部便于定位。
import { spawn } from 'node:child_process'
import fs from 'node:fs'
import http from 'node:http'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const root = path.resolve(here, '..', '..')
const binArg = process.argv.indexOf('--bin')
const BIN = binArg >= 0 ? path.resolve(process.argv[binArg + 1]) : path.join(root, 'tdebug.exe')

const log = (...a) => console.log('[smoke]', ...a)
const fail = (msg, extra = '') => {
  console.error('[smoke] ✗ ' + msg + (extra ? '\n' + extra : ''))
  process.exit(1)
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

function request(url, { method = 'GET', body } = {}) {
  return new Promise((resolve, reject) => {
    const u = new URL(url)
    const req = http.request(
      { hostname: u.hostname, port: u.port, path: u.pathname + u.search, method,
        headers: body ? { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) } : {} },
      (res) => {
        let data = ''
        res.on('data', (c) => (data += c))
        res.on('end', () => resolve({ status: res.statusCode, body: data }))
      },
    )
    req.on('error', reject)
    req.end(body)
  })
}

// 起一个 desktop 进程,返回 {child, ready, lines, exited}
function startDesktop({ dataDir, extraArgs = [] }) {
  if (!fs.existsSync(BIN)) fail(`未找到后端程序:${BIN}\n  请先执行: go build -o tdebug.exe .`)
  const cfg = path.join(dataDir, 'config.json')
  const child = spawn(BIN, ['desktop', '--config', cfg, '--listen', '127.0.0.1:0', '--json', ...extraArgs],
    { cwd: dataDir, windowsHide: true })
  const state = { child, ready: null, lines: [], exited: null }
  child.on('exit', (code) => { state.exited = code })
  const scan = (buf) => {
    for (const line of buf.toString('utf8').split(/\r?\n/)) {
      if (!line.trim()) continue
      state.lines.push(line)
      const i = line.indexOf('TDEBUG_READY ')
      if (i >= 0) {
        try { state.ready = JSON.parse(line.slice(i + 'TDEBUG_READY '.length)) } catch { /* 非 JSON 行忽略 */ }
      }
    }
  }
  child.stdout.on('data', scan)
  child.stderr.on('data', (b) => state.lines.push('[stderr] ' + b.toString('utf8').trim()))
  return state
}

async function waitReady(state, ms = 15000) {
  const t0 = Date.now()
  while (Date.now() - t0 < ms) {
    if (state.ready) return state.ready
    if (state.exited !== null) fail(`desktop 提前退出(code=${state.exited})`, state.lines.join('\n'))
    await sleep(100)
  }
  fail('15s 内没有 TDEBUG_READY', state.lines.join('\n'))
}

async function waitExit(state, ms = 8000) {
  const t0 = Date.now()
  while (Date.now() - t0 < ms) {
    if (state.exited !== null) return state.exited
    await sleep(100)
  }
  return null
}

const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'tdebug-desktop-smoke-'))
log('数据目录:', dataDir)
log('后端:', BIN)

let first = null
try {
  // ---- 1) 首启:空目录也能起来,并建出 config.json ----
  first = startDesktop({ dataDir })
  const ready = await waitReady(first)
  log('READY:', JSON.stringify(ready))
  if (ready.attached) fail('首启不应是 attached(应为新实例)', JSON.stringify(ready))
  if (!ready.url || !ready.pid) fail('READY 缺少 url/pid', JSON.stringify(ready))
  const cfg = path.join(dataDir, 'config.json')
  if (!fs.existsSync(cfg)) fail('未自动创建 config.json: ' + cfg)
  const stateFile = path.join(dataDir, '.tdebug-serve.json')
  if (!fs.existsSync(stateFile)) fail('未写状态文件: ' + stateFile)
  const st = JSON.parse(fs.readFileSync(stateFile, 'utf8'))
  if (st.url !== ready.url || st.pid !== ready.pid) fail('状态文件与 READY 不一致', JSON.stringify(st))
  log('✓ 首启建配置 + 写状态文件')

  // ---- 2) /api/status 与界面 HTML ----
  const status = JSON.parse((await request(ready.url + '/api/status')).body)
  if (status.server !== 'tdebug-debug') fail('status.server 不是 tdebug-debug: ' + status.server)
  const want = new URL(ready.url).host
  if (status.listen !== want) fail(`status.listen(${status.listen}) 与 READY(${want}) 不一致`)
  const home = await request(ready.url + '/')
  if (home.status !== 200 || !/<div id="root">/.test(home.body)) fail('GET / 未返回界面 HTML')
  log('✓ /api/status 真实端口一致;GET / 返回界面')

  // ---- 3) 同数据目录再起一个 → attached ----
  const second = startDesktop({ dataDir })
  const ready2 = await waitReady(second, 10000)
  if (!ready2.attached) fail('第二个实例应 attached:true', JSON.stringify(ready2))
  if (ready2.url !== ready.url) fail('attached 应指向同一实例', `${ready2.url} != ${ready.url}`)
  const code2 = await waitExit(second)
  if (code2 !== 0) fail(`attach 进程应正常退出,实际 code=${code2}`)
  log('✓ 已有实例时 attached:true 并退出(壳接管)')

  // ---- 4) 优雅停止 ----
  const sd = await request(ready.url + '/api/shutdown', { method: 'POST', body: '{}' })
  if (sd.status !== 200) fail('shutdown 应返回 200,实际 ' + sd.status)
  const code = await waitExit(first, 8000)
  if (code === null) fail('shutdown 后进程未退出', first.lines.join('\n'))
  if (fs.existsSync(stateFile)) fail('退出后状态文件未清理: ' + stateFile)
  log(`✓ /api/shutdown 优雅停止并清理状态文件(code=${code})`)

  log('全部通过 ✓')
  fs.rmSync(dataDir, { recursive: true, force: true })
} catch (e) {
  try { if (first?.child && first.exited === null) first.child.kill() } catch {}
  fail(String(e?.stack || e), first ? first.lines.join('\n') : '')
}
