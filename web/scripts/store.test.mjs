// store 状态机校验(纯状态,不连后端):
//   node 无浏览器环境 → 先补 window/localStorage/document,再动态载入 store
//   (store 初始化时会读 localStorage,静态 import 会先于补全局执行)
// 覆盖易回退的行为:
//   1. 点重放/启动的那一刻(后端响应之前)编辑器就必须在 loading,且旧调试现场已清空;
//   2. 旧会话(同目标复用宿主 → ID 与新会话相同)的收尾事件 idle/exit/dead
//      不得把刚置上的加载态清掉、也不得弹「后端断开」误报横幅,但宽限期过后必须放行;
//   3. 断点列表点击是「纯浏览跳转」:只发视口滚动信号,不搬动黄色停站高亮(currentLine)。
import assert from 'node:assert/strict'

globalThis.window = { setInterval: () => 1, clearInterval: () => {}, setTimeout: () => 1 }
globalThis.localStorage = { getItem: () => null, setItem: () => {}, removeItem: () => {} }
globalThis.document = { documentElement: { classList: { toggle: () => {} } } }

// 复用宿主:后端返回的 sessionId 与点击前是同一个(最容易被收尾事件误伤的路径)
let release = () => {}
globalThis.fetch = () => new Promise((res) => {
  release = () => res({
    ok: true, status: 200,
    json: async () => ({ ok: true, sessionId: 'sess-1', module: 'awsq990', prog: 'awsq990', runProg: 'awsq990' }),
  })
})

const { useStore } = await import('../src/store')
const S = () => useStore.getState()

useStore.setState({
  sessionId: 'sess-1', view: 'wslogs', loadingSource: false, state: 'stopped',
  sourceContent: 'MAIN\nEND MAIN', timeline: [{ t: '1', origin: 'system', kind: 'info', text: '旧会话日志' }],
  stop: { file: 'old.4gl', line: 7 }, frames: [{ n: 0, file: 'old.4gl', line: 7, func: 'main' }],
})

// 1) 点下去(await 之前)编辑器就应该已经在加载,且旧现场已清空
const p = S().replayStart('rowid-1')
assert.equal(S().view, 'debug', '应立即切到 debug 页')
assert.equal(S().loadingSource, true, '应立即进入加载态(不等后端响应)')
assert.equal(S().state, 'loading')
assert.equal(S().sessionId, null, '旧会话引用应立即解除')
assert.equal(S().sourceContent, '', '旧源码应清空')
assert.equal(S().timeline.length, 0, '旧时间线应清空')
assert.equal(S().stop, null)
assert.equal(S().launching, true)
console.log('OK 1) 点击瞬间即 loading,旧调试现场已清空')

// 2) 请求在途:旧会话的收尾事件(idle/exit/dead)不得清掉加载态
for (const ev of [
  { type: 'state', sessionId: 'sess-1', state: 'idle' },
  { type: 'state', sessionId: 'sess-1', state: 'exit' },
  { type: 'dead', sessionId: 'sess-1', text: 'SSH 连接断开' },
  { type: 'log', sessionId: 'sess-1', text: '重放调试启动,结束当前调试(会话保留)' },
]) S().onEvent(ev)
assert.equal(S().loadingSource, true, '在途收尾事件不得取消加载态')
assert.equal(S().state, 'loading')
assert.equal(S().backendDead, '', '不得弹出「后端断开」误报横幅')
assert.equal(S().timeline.length, 1, '普通日志(非收尾)仍应进入时间线')
console.log('OK 2) 在途旧会话收尾事件被丢弃,加载态与横幅不受影响')

// 3) 响应回来:绑定新会话,仍保持加载(等入口停站)
release()
await p
assert.equal(S().sessionId, 'sess-1')
assert.equal(S().loadingSource, true, '响应后仍应保持加载,等入口停站')
assert.equal(S().state, 'loading')
console.log('OK 3) 响应后绑定会话并保持加载')

// 4) 响应后 2s 宽限内迟到的收尾事件(WS 与 HTTP 竞态)同样不得清掉加载态
S().onEvent({ type: 'state', sessionId: 'sess-1', state: 'idle' })
S().onEvent({ type: 'dead', sessionId: 'sess-1', text: 'SSH 连接断开' })
assert.equal(S().loadingSource, true, '宽限期内迟到事件不得取消加载态')
assert.equal(S().backendDead, '')
console.log('OK 4) 响应后宽限期内迟到事件被丢弃')

// 5) 宽限期过后必须恢复放行(不能永久屏蔽真实退出,否则界面会卡在加载中)
const realNow = Date.now
Date.now = () => realNow() + 60_000
S().onEvent({ type: 'state', sessionId: 'sess-1', state: 'idle' })
Date.now = realNow
assert.equal(S().loadingSource, false, '宽限期过后真实 idle 必须生效')
assert.equal(S().state, 'idle')
console.log('OK 5) 宽限期结束后真实收尾事件正常生效')

// 6) 无旧会话时(首次从日志页重放)行为一致,且不做多余屏蔽
useStore.setState({ sessionId: null, view: 'wslogs', loadingSource: false, sourceContent: '' })
const p2 = S().replayStart('rowid-2')
assert.equal(S().loadingSource, true)
assert.equal(S().view, 'debug')
release()
await p2
assert.equal(S().sessionId, 'sess-1')
console.log('OK 6) 无旧会话时同样立即 loading')

// 7) 普通启动(launch)同样是「点下即 loading」,且清掉重放标记
useStore.setState({ sessionId: null, view: 'debug', loadingSource: false, lastReplayRowid: 'rowid-2' })
const p3 = S().launch('awsq990', 'awsq990')
assert.equal(S().loadingSource, true, 'launch 应立即进入加载态')
assert.equal(S().lastReplayRowid, null, '普通启动应清掉重放标记')
release()
await p3
console.log('OK 7) launch 同样点下即 loading')

// 8) 断点列表点击 = 纯浏览跳转:只发视口滚动信号,黄色停站高亮(currentLine)原地不动
useStore.setState({
  sessionId: 'sess-1', state: 'stopped', sourceDVM: 'awsq990.4gl',
  currentLine: 120, stop: { file: 'awsq990.4gl', line: 120 }, revealReq: null,
})
await S().jumpToBp({ num: 1, enabled: true, file: 'awsq990.4gl', line: 480, func: 'main' })
assert.equal(S().currentLine, 120, '黄色停站高亮必须留在真实停站行')
assert.equal(S().revealReq.line, 480, '应发出滚动到断点行的信号')
assert.equal(S().revealReq.nav, true, '断点跳转应标记为纯浏览跳转')
assert.equal(S().revealReq.key, 'debug')
// 运行中(idle/running)也允许跳转:只是滚视口,不动运行上下文
useStore.setState({ state: 'running' })
await S().jumpToBp({ num: 1, enabled: true, file: 'awsq990.4gl', line: 480, func: 'main' })
assert.equal(S().currentLine, 120, '运行中跳转同样不得改停站高亮')
assert.equal(S().revealReq.nav, true)
// 真停站定位仍必须搬动高亮(nav 默认 false)
S().reveal('debug', 500)
assert.equal(S().currentLine, 500, '停站/步进定位应照旧搬动黄色高亮')
assert.equal(S().revealReq.nav, false)
console.log('OK 8) 断点跳转只滚视口,黄色停站高亮不动')

console.log('\n全部通过: 8/8')
