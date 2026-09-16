// 桌面版开发启动器(无额外依赖):
//   1) 起前端 Vite(web/,端口 5173,热更新);
//   2) 起 Go 后端(固定 127.0.0.1:28670,匹配 web/vite.config.ts 的 /api 代理),
//      数据目录 desktop/.dev-data(独立于正式配置,随便折腾);
//   3) 等两者就绪后拉起 Electron,并让其加载 Vite 地址(TDEBUG_DEV_URL),
//      于是改前端代码即时生效,后端日志同时打到本终端。
// 退出:本进程收到退出/异常时一并结束两个子进程。
'use strict'

const { spawn, spawnSync } = require('node:child_process')
const fs = require('node:fs')
const http = require('node:http')
const path = require('node:path')

const root = path.resolve(__dirname, '..')
const dataDir = path.join(__dirname, '.dev-data')
const configPath = path.join(dataDir, 'config.json')
const logPath = path.join(dataDir, 'logs', 'dev.log')
const bin = process.env.TDEBUG_BIN ? path.resolve(process.env.TDEBUG_BIN) : path.join(root, 'tdebug.exe')
const LISTEN = process.env.TDEBUG_DESKTOP_PORT || '127.0.0.1:28670'
const VITE_URL = 'http://127.0.0.1:5173'

const kids = []
const log = (...a) => console.log('[dev]', ...a)

function probe(url, timeoutMs = 30000) {
  const t0 = Date.now()
  return new Promise((resolve, reject) => {
    const tick = () => {
      const req = http.get(url, (res) => { res.resume(); resolve() })
      req.on('error', () => {
        if (Date.now() - t0 > timeoutMs) reject(new Error(`等待 ${url} 就绪超时`))
        else setTimeout(tick, 300)
      })
    }
    tick()
  })
}

async function main() {
  if (!fs.existsSync(bin)) {
    console.error(`[dev] 未找到后端程序:${bin}\n[dev] 请先在仓库根目录执行:go build -o tdebug.exe .`)
    process.exit(1)
  }
  if (!fs.existsSync(path.join(__dirname, 'node_modules', 'electron', 'dist', 'electron.exe'))) {
    console.error('[dev] electron 二进制缺失(多见于 npm 拦下了依赖的 install 脚本)\n'
      + '[dev] 请执行:cd desktop && set ELECTRON_MIRROR=https://npmmirror.com/mirrors/electron/ && node node_modules\\electron\\install.js')
    process.exit(1)
  }
  fs.mkdirSync(path.dirname(logPath), { recursive: true })

  log(`启动后端 ${bin} desktop --listen ${LISTEN} (数据目录 ${dataDir})`)
  const logFd = fs.openSync(logPath, 'a')
  kids.push(spawn(bin, ['desktop', '--config', configPath, '--listen', LISTEN], {
    cwd: dataDir,
    stdio: ['ignore', logFd, logFd],
    env: { ...process.env, TDEBUG_SERVE_LOG: logPath },
  }))
  // 后端真实地址从状态文件读:端口被占用时它会顺延,不能假定还是 LISTEN
  const serveURL = await waitServeURL()
  log(`后端就绪:${serveURL}`)

  log(`启动前端 Vite(web/),/api 代理到 ${serveURL}`)
  kids.push(spawn('npm', ['run', 'dev'], {
    cwd: path.join(root, 'web'),
    shell: true,
    stdio: 'inherit',
    env: { ...process.env, TDEBUG_PROXY: serveURL },
  }))
  await probe(VITE_URL)
  log('两边就绪,启动 Electron(加载 ' + VITE_URL + ')')

  const env = { ...process.env, TDEBUG_DEV_URL: VITE_URL, TDEBUG_SERVE_URL: serveURL }
  delete env.ELECTRON_RUN_AS_NODE
  const electron = spawnSync(require('electron'), ['.'], { cwd: __dirname, stdio: 'inherit', env })
  shutdown()
  process.exit(electron.status ?? 0)
}

// 读 <数据目录>/.tdebug-serve.json 拿后端真实地址(与 CLI 同一套寻址规则)。
async function waitServeURL(timeoutMs = 30000) {
  const file = path.join(dataDir, '.tdebug-serve.json')
  const t0 = Date.now()
  while (Date.now() - t0 < timeoutMs) {
    try {
      const st = JSON.parse(fs.readFileSync(file, 'utf8'))
      if (st.url) return st.url
    } catch { /* 还没写出来 */ }
    await new Promise((r) => setTimeout(r, 300))
  }
  throw new Error(`后端未就绪(未写出 ${file}),见日志 ${logPath}`)
}

let closing = false
function shutdown() {
  if (closing) return
  closing = true
  for (const k of kids) {
    try {
      // Windows 上 npm 是包装进程,kill 它不会带走 Vite/后端:按进程树杀
      spawnSync('taskkill', ['/PID', String(k.pid), '/T', '/F'], { stdio: 'ignore' })
    } catch { /* 已退出 */ }
    try { k.kill() } catch { /* 已退出 */ }
  }
}
process.on('SIGINT', () => { shutdown(); process.exit(0) })
process.on('SIGTERM', () => { shutdown(); process.exit(0) })
process.on('exit', shutdown)

main().catch((e) => { console.error('[dev] ' + (e.stack || e)); shutdown(); process.exit(1) })
