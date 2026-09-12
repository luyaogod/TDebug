import { useEffect, useRef, useState, type ReactNode } from 'react'
import { RotateCcw, WifiOff, Bug, Globe, FlaskConical, Settings, X, ListTree, Sword, Server, type LucideIcon } from 'lucide-react'
import { connectWS, useStore } from './store'
import { Toolbar } from './Toolbar'
import { SourceView, editorRef } from './SourceView'
import { RightPanels, TimelinePanel } from './Panels'
import { OutlinePanel } from './OutlinePanel'
import { SessionPanel } from './SessionPanel'
import { WsLogView } from './WsLogView'
import { WsTestView } from './WsTestView'
import { SettingsView } from './SettingsView'
import { StatusBar } from './StatusBar'
import { api } from './api'

// VS Code 风格活动栏图标按钮:通栏占满活动栏宽度(悬停/选中底色左右贴边,无留白差),
// 选中态灰色底色(无左侧蓝条),未选中透明显灰
function ActivityIcon({ icon: Icon, label, active, onClick }: {
  icon: LucideIcon; label: string; active: boolean; onClick: () => void
}) {
  return (
    <button title={label} onClick={onClick}
      className={`flex h-8 w-full shrink-0 items-center justify-center transition-colors ${
        active ? 'bg-foreground/10 text-foreground' : 'text-muted-foreground hover:text-foreground'
      }`}>
      <Icon className="h-5 w-5" />
    </button>
  )
}

// 单页面应用 keep-alive:视图首次访问后常驻挂载,切换只改 display,不卸载重挂。
// 编辑器(Monaco)实例、源码页签、表单输入、滚动位置等状态全部保留,切换零闪烁、
// 零重载——这正是"切换页面不像网页那样重新加载"的实现。
function ViewPane({ show, children }: { show: boolean; children: ReactNode }) {
  const [mounted, setMounted] = useState(show)
  useEffect(() => { if (show) setMounted(true) }, [show])
  if (!mounted) return null
  return (
    <div className={`min-h-0 min-w-0 flex-1 ${show ? 'flex' : 'hidden'}`}>
      {children}
    </div>
  )
}

// 浏览器标签页标题反馈:停站时标题闪烁(仅后台),运行/启动改前缀。
// 停站标题在「⚠ 停站 file:line」与原名之间每秒交替,后台标签会被浏览器高亮提醒。
function useDocTitleBlink() {
  const sessionId = useStore((s) => s.sessionId)
  const state = useStore((s) => s.state)
  const file = useStore((s) => s.stop?.file)
  const line = useStore((s) => s.stop?.line)
  useEffect(() => {
    const base = 'TDebug'
    const setT = (t: string) => { document.title = t }
    if (!sessionId) { setT(base); return }
    let timer: number | undefined
    if (state === 'stopped') {
      const loc = file && line ? `${file}:${line}` : '已停站'
      const flashA = `⚠ 停站 ${loc}`
      setT(flashA)
      let toggle = true
      const onVis = () => {
        if (document.hidden) {
          toggle = true
          timer = window.setInterval(() => {
            document.title = toggle ? base : flashA
            toggle = !toggle
          }, 1000)
        } else {
          if (timer) { clearInterval(timer); timer = undefined }
          setT(flashA)
        }
      }
      document.addEventListener('visibilitychange', onVis)
      if (document.hidden) onVis()
      return () => {
        document.removeEventListener('visibilitychange', onVis)
        if (timer) clearInterval(timer)
        setT(base)
      }
    }
    if (state === 'running') setT(`▶ 运行中 · ${base}`)
    else if (state === 'loading') setT(`⏳ 启动中 · ${base}`)
    else setT(base)
  }, [sessionId, state, file, line])
}

export function App() {
  useDocTitleBlink()
  useEffect(() => {
    const close = connectWS()
    void api.status().catch(() => {})
    void useStore.getState().adoptExisting()
    // 调试快捷键:F5 继续 / F10 步过 / F11 步入 / Shift+F11 步出
    const onKey = (e: KeyboardEvent) => {
      const st = useStore.getState()
      if (!st.sessionId) return
      const stopped = st.state === 'stopped'
      if (e.key === 'F5') { e.preventDefault(); if (stopped) void st.control('continue') }
      else if (e.key === 'F10') { e.preventDefault(); if (stopped) void st.control('next') }
      else if (e.key === 'F11') {
        e.preventDefault()
        if (stopped) void st.control(e.shiftKey ? 'finish' : 'step')
      }
    }
    window.addEventListener('keydown', onKey)
    return () => { window.removeEventListener('keydown', onKey); close() }
  }, [])

  const showRight = useStore((s) => s.showRight)
  const showBottom = useStore((s) => s.showBottom)
  const rightView = useStore((s) => s.rightView)
  const setRightView = useStore((s) => s.setRightView)
  const view = useStore((s) => s.view)
  const setView = useStore((s) => s.setView)
  const launchError = useStore((s) => s.launchError)
  const setLaunchError = useStore((s) => s.setLaunchError)
  const backendDead = useStore((s) => s.backendDead)
  const wsConnected = useStore((s) => s.wsConnected)
  const launching = useStore((s) => s.launching)
  const sessionId = useStore((s) => s.sessionId)
  // 注意:sessionId 必须是独立 hook 调用——嵌在 || 条件里会在短路时少调用一次,
  // 违反 React Hooks 规则导致 hook 队列错乱(Should have a queue / 无限重渲染)
  const banner = backendDead || (sessionId && !wsConnected ? '服务连接断开,自动重连中…' : '') 
  const restart = useStore((s) => s.restart)

  // 侧边栏/代码区宽度拖拽(记忆到 localStorage)
  const [panelW, setPanelW] = useState(() => {
    const v = Number(localStorage.getItem('tdebug.panelW'))
    return v >= 240 && v <= 640 ? v : 320
  })
  // keep-alive 视图切换下,Monaco 容器被 display:none 期间 ResizeObserver 会拿到 0 尺寸;
  // 切回调试视图后补一次 layout(),确保编辑器渲染与视口不残留错位
  useEffect(() => {
    if (view !== 'debug') return
    const t = setTimeout(() => editorRef.current?.layout(), 60)
    return () => clearTimeout(t)
  }, [view])

  const onResizeDown = (e: React.MouseEvent<HTMLDivElement>) => {
    e.preventDefault()
    const el = e.currentTarget
    el.classList.add('dragging')
    const startX = e.clientX
    const startW = panelW
    let latest = startW
    const move = (ev: MouseEvent) => {
      latest = Math.min(640, Math.max(240, startW - (ev.clientX - startX)))
      setPanelW(latest)
    }
    const up = () => {
      el.classList.remove('dragging')
      localStorage.setItem('tdebug.panelW', String(latest))
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  // 下方面板(操作时间线/协议流)高度拖拽(记忆到 localStorage)
  const [bottomH, setBottomH] = useState(() => {
    const v = Number(localStorage.getItem('tdebug.bottomH'))
    return v >= 120 && v <= 600 ? v : 192
  })
  const onBottomResizeDown = (e: React.MouseEvent<HTMLDivElement>) => {
    e.preventDefault()
    const el = e.currentTarget
    el.classList.add('dragging')
    const startY = e.clientY
    const startH = bottomH
    let latest = startH
    const move = (ev: MouseEvent) => {
      // 分隔线跟手:向上拖 = 面板变高(与 VS Code 底部面板一致)
      latest = Math.min(600, Math.max(120, startH - (ev.clientY - startY)))
      setBottomH(latest)
    }
    const up = () => {
      el.classList.remove('dragging')
      localStorage.setItem('tdebug.bottomH', String(latest))
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  // 布局:上 Toolbar / 下 StatusBar 整条;中部 = 活动栏 + [中间列(编辑区/时间线上下) + 整高右面板]
  return (
    <div className="flex h-screen flex-col bg-background text-foreground">
      <Toolbar />
      {/* 启动失败横幅:显眼红色,可关闭;下次启动/下次成功时自动清除 */}
      {launchError && (
        <div className="flex shrink-0 items-center gap-2 border-b border-red-500/20 bg-red-500/10 px-3 py-1.5 text-xs text-red-600 dark:text-red-400">
          <span className="min-w-0 flex-1 truncate" title={launchError}>{launchError}</span>
          <button onClick={() => setLaunchError('')} title="关闭"
            className="p-0.5 transition-colors hover:bg-red-500/20">
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      )}
      {banner && (
        <div className="flex shrink-0 items-center gap-2 border-b border-red-500/20 bg-red-500/10 px-3 py-1.5 text-xs text-red-600 dark:text-red-600 dark:text-red-400">
          <WifiOff className="h-3.5 w-3.5" />
          <span>{banner}</span>
          {backendDead && (
            <button
              className="ml-auto inline-flex items-center gap-1 border border-red-500/30 px-2 py-0.5 hover:bg-red-500/10 disabled:opacity-40"
              disabled={launching}
              onClick={() => void restart()}
            >
              <RotateCcw className="h-3 w-3" />
              {launching ? '重启中…' : '重新启动'}
            </button>
          )}
        </div>
      )}
      <div className="flex min-h-0 flex-1">
        {/* VS Code 经典活动栏:与侧栏同底色,右缘 1px 分割线与内容区分隔 */}
        <div className="flex w-10 shrink-0 flex-col items-center gap-1 border-r border-border bg-background py-2">
          <ActivityIcon icon={Bug} label="调试" active={view === 'debug'} onClick={() => setView('debug')} />
          <ActivityIcon icon={Globe} label="接口日志" active={view === 'wslogs'} onClick={() => setView('wslogs')} />
          <ActivityIcon icon={FlaskConical} label="服务测试" active={view === 'wstest'} onClick={() => setView('wstest')} />
          <ActivityIcon icon={Settings} label="设置" active={view === 'settings'} onClick={() => setView('settings')} />
        </div>
        {/* 四个视图全部 keep-alive:首次访问后常驻挂载,切换仅改 display */}
        <ViewPane show={view === 'debug'}>
          {/* 面板紧贴(VS Code 经典密度):无外边距,仅靠 sash 分割线分区
             min-w-0 + overflow-hidden:Monaco 会给编辑器写内联像素宽度,
             否则 flex 最小宽度被钉死,收起再展开时编辑区不回缩、右面板被挤出屏幕 */}
          <div className="flex min-h-0 min-w-0 flex-1 overflow-hidden">
            <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
              <div className="min-h-0 flex-1 overflow-hidden bg-card">
                <SourceView />
              </div>
              {showBottom && (
                <div className="flex shrink-0 flex-col">
                  <div className="row-resizer" onMouseDown={onBottomResizeDown} title="拖拽调整下方面板高度" />
                  <div style={{ height: bottomH }} className="shrink-0">
                    <TimelinePanel />
                  </div>
                </div>
              )}
            </div>
            {showRight && (
              <>
                  <div className="col-resizer" onMouseDown={onResizeDown} title="拖拽调整代码区与侧边栏宽度" />
                <div style={{ width: panelW }} className="min-h-0 shrink-0">
                  {/* 多 sheet keep-alive:切换仅改 display,保留手风琴展开/监视输入等本地状态 */}
                  <div className={rightView === 'session' ? 'h-full min-h-0 w-full' : 'hidden'}>
                    <SessionPanel />
                  </div>
                  <div className={rightView === 'debug' ? 'h-full min-h-0 w-full' : 'hidden'}>
                    <RightPanels />
                  </div>
                  <div className={rightView === 'outline' ? 'h-full min-h-0 w-full' : 'hidden'}>
                    <OutlinePanel />
                  </div>
                </div>
                {/* 右侧 sheet 切换栏(仿左侧活动栏):与右侧边栏一同受折叠按钮控制
                    —— 顺序即主次:会话(选环境/看状态)→ 调试面板 → 大纲 */}
                <div className="flex w-10 shrink-0 flex-col border-l border-border bg-background py-2">
                  <ActivityIcon icon={Server} label="会话" active={rightView === 'session'} onClick={() => setRightView('session')} />
                  <ActivityIcon icon={Sword} label="调试面板" active={rightView === 'debug'} onClick={() => setRightView('debug')} />
                  <ActivityIcon icon={ListTree} label="大纲" active={rightView === 'outline'} onClick={() => setRightView('outline')} />
                </div>
              </>
            )}
          </div>
        </ViewPane>
        <ViewPane show={view === 'wslogs'}>
          <WsLogView />
        </ViewPane>
        <ViewPane show={view === 'wstest'}>
          <WsTestView />
        </ViewPane>
        <ViewPane show={view === 'settings'}>
          <SettingsView />
        </ViewPane>
      </div>
      <StatusBar />
    </div>
  )
}
