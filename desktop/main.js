// TDebug 桌面版主进程(Electron 外壳)。
//
// 设计要点:桌面版不复制任何界面资源 —— 界面与 API 都来自同一份 Go 二进制
// (go:embed web/dist)。主进程只做三件事:
//   1) 拉起 `tdebug.exe desktop`(自建数据目录/配置,前台运行),读 stdout 的
//      TDEBUG_READY {json} 拿到真实地址(端口可能顺延);
//   2) 用 BrowserWindow 加载该地址;窗口位置/大小记忆在数据目录;
//   3) 关窗时 POST /api/shutdown 让 Go 侧优雅收尾(会话/SSH 连接收口),
//      失败再兜底 kill;若是接管(attached)别人的实例则不去停它。
//
// 数据目录:便携版 = exe 所在目录(electron-builder 注入 PORTABLE_EXECUTABLE_DIR,
// 与 CLI 便携包"exe + config.json 同目录"一致);安装版 = %APPDATA%\TDebug。
// 环境变量:TDEBUG_DESKTOP_DATA 指定数据目录、TDEBUG_BIN 指定后端、TDEBUG_DESKTOP_PORT
// 覆盖监听地址、TDEBUG_DEV_URL 走开发模式(载入 Vite,不自己拉后端)。
'use strict'

const { app, BrowserWindow, Menu, dialog, screen, shell } = require('electron')
const { spawn } = require('node:child_process')
const fs = require('node:fs')
const http = require('node:http')
const path = require('node:path')

const READY_MARK = 'TDEBUG_READY '
const READY_TIMEOUT_MS = 30000
const LOG_MAX_BYTES = 2 * 1024 * 1024

const DEV_URL = process.env.TDEBUG_DEV_URL || ''
const DEV_SERVE_URL = process.env.TDEBUG_SERVE_URL || 'http://127.0.0.1:28670'

let win = null
let child = null
let childAttached = false // 接管既有实例(或开发模式下后端由 dev.js 拉起):退出时不停它
let service = null // { url, pid, config, log, attached }
let quitting = false

// ---------- 路径 ----------

// 绿色(便携)版判定:程序目录里带 .portable 标记(打包 zip 时放进去),
// 或那里已经有配置/日志/状态文件(用户删了标记也不会"配置突然不见了")。
// electron-builder 的 portable 目标会注入 PORTABLE_EXECUTABLE_DIR,同样按绿色版处理。
// 绿色版:配置/日志/断点都放 exe 同目录 —— 与 CLI 便携包(tdebug.exe + config.json)一致,
// 两种便携包可以共用同一个 config.json。安装版:走 %APPDATA%\TDebug。
function portableRoot() {
  if (process.env.PORTABLE_EXECUTABLE_DIR) return path.resolve(process.env.PORTABLE_EXECUTABLE_DIR)
  if (!app.isPackaged) return '' // 开发态不按绿色版算(那是仓库目录)
  const dir = path.dirname(app.getPath('exe'))
  const marks = ['.portable', 'config.json', 'logs', '.tdebug-serve.json']
  return marks.some((m) => fs.existsSync(path.join(dir, m))) ? dir : ''
}

function dataDir() {
  if (process.env.TDEBUG_DESKTOP_DATA) return path.resolve(process.env.TDEBUG_DESKTOP_DATA)
  return portableRoot() || app.getPath('userData')
}
function configPath() { return path.join(dataDir(), 'config.json') }
function logPath() { return path.join(dataDir(), 'logs', 'desktop.log') }
function windowStatePath() { return path.join(dataDir(), 'window-state.json') }

function backendPath() {
  if (process.env.TDEBUG_BIN) return path.resolve(process.env.TDEBUG_BIN)
  if (app.isPackaged) return path.join(process.resourcesPath, 'tdebug.exe')
  return path.join(__dirname, '..', 'tdebug.exe')
}

// ---------- 后端进程 ----------

let logStream = null
function openLog() {
  const p = logPath()
  fs.mkdirSync(path.dirname(p), { recursive: true })
  try {
    const st = fs.statSync(p)
    if (st.size > LOG_MAX_BYTES) fs.renameSync(p, p + '.1') // 只保留一份上一轮日志
  } catch { /* 不存在则忽略 */ }
  logStream = fs.createWriteStream(p, { flags: 'a' })
  return p
}
function writeLog(line) {
  const ts = new Date().toLocaleString('zh-CN', { hour12: false })
  try { logStream?.write(`[${ts}] ${line}\n`) } catch { /* 日志失败不影响使用 */ }
}
// 日志流只开一次(启动时就开,便于记录"后端还没起来"之前的启动信息;重启后端不重复轮转)
function ensureLog() {
  return logStream ? logPath() : openLog()
}
function logTail(n = 25) {
  try {
    const lines = fs.readFileSync(logPath(), 'utf8').trim().split(/\r?\n/)
    return lines.slice(-n).join('\n')
  } catch { return '(无日志)' }
}

function startBackend() {
  const bin = backendPath()
  if (!fs.existsSync(bin)) {
    throw new Error(`未找到后端程序:\n${bin}\n\n开发态请先在仓库根目录执行:\n  go build -o tdebug.exe .\n\n打包态说明安装包不完整,请重新安装。`)
  }
  const dir = dataDir()
  fs.mkdirSync(dir, { recursive: true })
  const log = ensureLog()
  const args = ['desktop', '--config', configPath()]
  if (process.env.TDEBUG_DESKTOP_PORT) args.push('--listen', process.env.TDEBUG_DESKTOP_PORT)

  writeLog(`启动后端: ${bin} ${args.join(' ')}`)
  child = spawn(bin, args, {
    cwd: dir,
    windowsHide: true,
    stdio: ['ignore', 'pipe', 'pipe'],
    env: { ...process.env, TDEBUG_SERVE_LOG: log },
  })

  let buffer = ''
  const onChunk = (chunk) => {
    buffer += chunk.toString('utf8')
    const lines = buffer.split(/\r?\n/)
    buffer = lines.pop() ?? ''
    for (const line of lines) {
      if (!line.trim()) continue
      writeLog(line)
      if (process.env.TDEBUG_DEBUG) console.log('[backend]', line)
      const i = line.indexOf(READY_MARK)
      if (i >= 0 && !service) {
        try {
          service = JSON.parse(line.slice(i + READY_MARK.length))
          writeLog(`就绪: ${service.url} (pid ${service.pid}${service.attached ? ', 接管既有实例' : ''})`)
          if (service.attached) childAttached = true
          readySettle?.resolve(service)
        } catch (e) {
          writeLog(`READY 行解析失败: ${e.message}`)
        }
      }
    }
  }
  child.stdout.on('data', onChunk)
  child.stderr.on('data', onChunk)
  child.on('error', (err) => { writeLog(`后端进程错误: ${err.message}`); readySettle?.reject(err) })
  child.on('exit', (code, signal) => {
    writeLog(`后端进程退出 code=${code} signal=${signal}`)
    if (!service) readySettle?.reject(new Error(`后端启动失败(code=${code})`))
    else if (!quitting) {
      // 运行中后端掉了:提示并允许重启(不静默白屏)
      dialog.showMessageBox(win, {
        type: 'error',
        title: '调试服务已停止',
        message: `本地调试服务意外退出(code=${code})。`,
        detail: logTail(12),
        buttons: ['重启服务', '退出'],
        defaultId: 0,
        cancelId: 1,
      }).then((r) => { if (r.response === 0) restartBackend(); else app.quit() })
    }
  })
  return waitReady()
}

// READY 行的等待:成功/失败都清掉定时器并只结算一次
let readySettle = null
function waitReady() {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      readySettle = null
      reject(new Error(`后端 ${READY_TIMEOUT_MS / 1000}s 内未就绪\n\n日志:\n${logTail(15)}`))
    }, READY_TIMEOUT_MS)
    readySettle = {
      resolve: (v) => { clearTimeout(timer); readySettle = null; resolve(v) },
      reject: (e) => { clearTimeout(timer); readySettle = null; reject(e) },
    }
  })
}

function postShutdown(timeoutMs = 3000) {
  return new Promise((resolve, reject) => {
    if (!service?.url) return reject(new Error('无服务地址'))
    const u = new URL(service.url)
    const body = '{}'
    const req = http.request({
      hostname: u.hostname, port: u.port, path: '/api/shutdown', method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) },
      timeout: timeoutMs,
    }, (res) => { res.resume(); resolve(res.statusCode) })
    req.on('timeout', () => req.destroy(new Error('shutdown 超时')))
    req.on('error', reject)
    req.end(body)
  })
}

async function stopBackend() {
  if (childAttached || !child) return
  try {
    const code = await postShutdown(3000)
    writeLog(`已请求优雅停止(HTTP ${code})`)
  } catch (e) {
    writeLog(`优雅停止失败,改为结束进程: ${e.message}`)
  }
  if (child && child.exitCode === null) {
    const done = new Promise((r) => child.once('exit', r))
    const timer = new Promise((r) => setTimeout(r, 2500))
    await Promise.race([done, timer])
    if (child.exitCode === null) { try { child.kill() } catch { /* 已退出 */ } }
  }
}

async function restartBackend() {
  if (child && child.exitCode === null) { try { child.kill() } catch { /* ignore */ } }
  child = null
  childAttached = false
  service = null
  try {
    const s = await startBackend()
    if (process.env.TDEBUG_DEV_URL) win.loadURL(DEV_URL)
    else win.loadURL(s.url)
  } catch (e) {
    dialog.showErrorBox('重启服务失败', String(e.message || e))
    app.quit()
  }
}

// ---------- 窗口 ----------

function loadWindowState() {
  try {
    const s = JSON.parse(fs.readFileSync(windowStatePath(), 'utf8'))
    if (typeof s.width !== 'number' || typeof s.height !== 'number') return {}
    // 屏幕拔插后旧坐标可能落在显示器之外:与任一显示器工作区有交集才复用
    if (typeof s.x === 'number' && typeof s.y === 'number') {
      const visible = screen.getAllDisplays().some((d) => {
        const a = d.workArea
        return s.x < a.x + a.width && s.x + s.width > a.x && s.y < a.y + a.height && s.y + s.height > a.y
      })
      if (!visible) { delete s.x; delete s.y }
    }
    return s
  } catch { return {} }
}

let saveStateTimer = null
function saveWindowState() {
  if (!win || win.isDestroyed()) return
  clearTimeout(saveStateTimer)
  saveStateTimer = setTimeout(() => {
    if (!win || win.isDestroyed()) return
    try {
      const b = win.getNormalBounds()
      fs.writeFileSync(windowStatePath(), JSON.stringify({ ...b, maximized: win.isMaximized() }, null, 2))
    } catch { /* 记忆窗口位置失败不影响使用 */ }
  }, 400)
}

function createWindow() {
  const st = loadWindowState()
  win = new BrowserWindow({
    width: st.width || 1440,
    height: st.height || 900,
    x: st.x,
    y: st.y,
    minWidth: 1024,
    minHeight: 640,
    show: false,
    title: 'TDebug',
    backgroundColor: '#181818',
    autoHideMenuBar: true,
    icon: path.join(__dirname, 'build', 'icon.ico'),
    webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true, spellcheck: false },
  })
  if (st.maximized) win.maximize()
  win.once('ready-to-show', () => win.show())
  win.on('resize', saveWindowState)
  win.on('move', saveWindowState)
  win.on('close', saveWindowState)
  win.on('closed', () => { win = null })

  // 站内导航放行,外链交系统浏览器;窗口内不开新窗口
  const localOrigin = () => {
    try { return new URL(process.env.TDEBUG_DEV_URL || service?.url || DEV_SERVE_URL).origin } catch { return '' }
  }
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https?:/i.test(url)) shell.openExternal(url)
    return { action: 'deny' }
  })
  win.webContents.on('will-navigate', (e, url) => {
    const origin = localOrigin()
    if (origin && !url.startsWith(origin)) {
      e.preventDefault()
      if (/^https?:/i.test(url)) shell.openExternal(url)
    }
  })
  return win
}

// ---------- 快捷键(不挂原生菜单栏) ----------

// 原生菜单栏已移除(界面本身就是应用工具栏,菜单栏多余且难看)。只保留窗口内的
// 常用快捷键——注意别和调试器的 F5/F10/F11(继续/步过/步入)冲突,所以重载只用 Ctrl+R。
function bindShortcuts(w) {
  const isDev = process.env.TDEBUG_DEV_URL
  w.webContents.on('before-input-event', (e, input) => {
    if (input.type !== 'keyDown') return
    const ctrl = input.control || input.meta
    const key = input.key
    if (key === 'F12' || (ctrl && input.shift && (key === 'I' || key === 'i'))) {
      e.preventDefault()
      w.webContents.toggleDevTools()
      return
    }
    if (ctrl && (key === 'R' || key === 'r')) {
      e.preventDefault()
      if (input.shift) w.webContents.reloadIgnoringCache()
      else w.webContents.reload()
      return
    }
    if (ctrl && (key === '=' || key === '+')) { e.preventDefault(); w.webContents.setZoomLevel(w.webContents.getZoomLevel() + 0.5); return }
    if (ctrl && key === '-') { e.preventDefault(); w.webContents.setZoomLevel(w.webContents.getZoomLevel() - 0.5); return }
    if (ctrl && key === '0') { e.preventDefault(); w.webContents.setZoomLevel(0); return }
    if (ctrl && (key === 'Q' || key === 'q')) { e.preventDefault(); app.quit(); return }
    // 排障入口(不占菜单):Ctrl+Shift+D 打开配置目录,Ctrl+Shift+L 打开日志
    if (isDev || !ctrl || !input.shift) return
    if (key === 'D' || key === 'd') { e.preventDefault(); fs.mkdirSync(dataDir(), { recursive: true }); shell.openPath(dataDir()) }
    if (key === 'L' || key === 'l') {
      e.preventDefault()
      if (fs.existsSync(logPath())) shell.openPath(logPath())
      else { fs.mkdirSync(dataDir(), { recursive: true }); shell.openPath(dataDir()) }
    }
  })
}

// ---------- 启动 ----------

async function boot() {
  ensureLog() // 先开日志:窗口/菜单/后端等启动信息都要落盘
  createWindow()
  bindShortcuts(win)
  writeLog(`原生菜单栏: ${Menu.getApplicationMenu() ? '已启用' : '已移除(界面自带工具栏)'}`)
  try {
    let url
    if (DEV_URL) {
      // 开发模式:Vite(5173)+ dev.js 拉起的后端(28670,配合 vite proxy);本进程不拉后端
      childAttached = true
      service = { url: DEV_SERVE_URL, dev: true }
      url = DEV_URL
      writeLog(`开发模式:加载 ${url},后端 ${DEV_SERVE_URL}(由 dev.js 管理)`)
    } else {
      const s = await startBackend()
      url = s.url
    }
    await win.loadURL(url)
    // 没有配置时不再弹原生对话框:界面自己会落到「设置 → 环境」(见 web 侧启动检查)
  } catch (e) {
    writeLog(`启动失败: ${e.message}`)
    dialog.showErrorBox('TDebug 启动失败', `${e.message}\n\n日志:${logPath()}`)
    app.quit()
  }
}

if (!app.requestSingleInstanceLock()) {
  app.quit()
} else {
  app.on('second-instance', () => {
    if (win) {
      if (win.isMinimized()) win.restore()
      win.focus()
    }
  })
  app.setAppUserModelId('com.tdebug.desktop')
  app.whenReady().then(() => {
    // 彻底移除原生菜单栏(界面自己就是工具栏;留空菜单栏 Alt 还会弹出来)
    Menu.setApplicationMenu(null)
    return boot()
  })
  app.on('window-all-closed', () => app.quit())
  app.on('before-quit', (e) => {
    if (quitting) return
    e.preventDefault()
    quitting = true
    saveWindowState()
    stopBackend().finally(() => app.quit())
  })
}
