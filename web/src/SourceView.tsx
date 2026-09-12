// 源码视图:VS Code 式多页签 = 调试页(锁定第一个,跟随停站)+ 浏览页(Ctrl+点击函数等静态打开)
// 单 Editor 实例,path 切换复用/重建 monaco model(@monaco-editor/react 自动保存恢复视口)
import { useEffect, useMemo, useRef, useState } from 'react'
import Editor, { type OnMount } from '@monaco-editor/react'
import { Bug, Loader2, X } from 'lucide-react'
import * as monaco from 'monaco-editor'
import { useStore } from './store'
import { cn } from './lib/utils'
import { attachHover } from './fglHover'
import { setupMonaco, monacoThemeName } from './monacoSetup'
import { progKey } from './fglPath'

export const editorRef = { current: null as monaco.editor.IStandaloneCodeEditor | null }

// Monaco 初始化(语言/主题/worker)在 monacoSetup.ts:与报文编辑器共用,模块加载时立即配置
setupMonaco()

export function SourceView() {
  // 调试页数据
  const sourceContent = useStore((s) => s.sourceContent)
  const sourceDVM = useStore((s) => s.sourceDVM)
  const currentLine = useStore((s) => s.currentLine)
  const breakpoints = useStore((s) => s.breakpoints)
  const state = useStore((s) => s.state)
  const stop = useStore((s) => s.stop)
  const module = useStore((s) => s.module)
  const theme = useStore((s) => s.theme)
  const loadingSource = useStore((s) => s.loadingSource)
  const replayRowid = useStore((s) => s.lastReplayRowid)
  const prog = useStore((s) => s.prog)
  // 页签
  const tabs = useStore((s) => s.tabs)
  const activeTab = useStore((s) => s.activeTab)
  const setActiveTab = useStore((s) => s.setActiveTab)
  const closeTab = useStore((s) => s.closeTab)

  const active = tabs.find((t) => t.key === activeTab)
  const isDebug = !active

  // 当前编辑器展示内容(调试页 vs 浏览页)
  // 行号校准:offset > 0 时在源码顶部前插 offset 个空行,让 Monaco 行号与 fgldb(DVM)行号
  // 对齐(协议流与编辑器不再错位;断点/停站仍按 DVM 行号自然工作)
  const lineOffset = useStore((s) => s.lineOffset)
  const content = isDebug
    ? (lineOffset > 0 ? '\n'.repeat(lineOffset) + (sourceContent || '') : (sourceContent || ''))
    : (active!.content || (active!.missing ? '' : ''))
  const modelPath = isDebug ? 'debug:' + (sourceDVM || prog) : 'tab:' + active!.file
  const cursorLine = isDebug ? currentLine : (active!.line ?? 0)

  const [editorReady, setEditorReady] = useState(false)
  const [modelTick, setModelTick] = useState(0) // 页签切换 = model 切换完成后重画装饰/滚动
  const decosRef = useRef<monaco.editor.IEditorDecorationsCollection | null>(null)

  const onMount: OnMount = (editor) => {
    editorRef.current = editor
    decosRef.current = editor.createDecorationsCollection([])
    setEditorReady(true)
    // 变量悬浮取值卡片(仅 debug model、仅停站时取值)
    attachHover(editor)
    // 点击行号/边栏切换断点
    editor.onMouseDown((e) => {
      const t = e.target.type
      if (t !== monaco.editor.MouseTargetType.GUTTER_GLYPH_MARGIN && t !== monaco.editor.MouseTargetType.GUTTER_LINE_NUMBERS) return
      const line = e.target.position?.lineNumber
      if (line) useStore.getState().toggleBreakpoint(line)
    })
    // 切换页签(@monaco-editor/react 换 model)后装饰集合要重新应用到新 model。
    // 此处不做滚动补偿:视口滚动只由 revealReq 定位信号驱动,纯页签切换保持离开时视口
    editor.onDidChangeModel(() => setModelTick((t) => t + 1))

    // Ctrl+悬停/点击跳函数(类 VS Code,全自管理):
    // 悬停 = 光标下的词变蓝+下划线+手形光标,同时异步预定位(fgldb info line)缓存结果;
    // 点击 = 命中缓存的词才跳转。不用 Monaco definitionProvider——它在按下 Ctrl 的
    // 检测阶段就会被调用,会造成「还没点左键就跳走」
    const fnDecos = editor.createDecorationsCollection([])
    let ctrlDown = false
    let lastWord = ''
    let pendingDef: { word: string; file: string; line: number } | null = null
    const locateCache = new Map<string, { file: string; line: number } | null>()
    const clearHover = () => { lastWord = ''; fnDecos.set([]) }
    const trackKey = (e: KeyboardEvent) => {
      const down = e.ctrlKey
      if (down !== ctrlDown) {
        ctrlDown = down
        if (!down) clearHover()
      }
    }
    window.addEventListener('keydown', trackKey)
    window.addEventListener('keyup', trackKey)
    window.addEventListener('blur', () => { ctrlDown = false; clearHover() })
    const hoverLocate = (word: string) => {
      const st = useStore.getState()
      if (!st.sessionId || st.state !== 'stopped') return
      if (locateCache.has(word)) { pendingDef = locateCache.get(word) ? { word, ...locateCache.get(word)! } : null; return }
      void st.locate(word).then((r) => {
        locateCache.set(word, r.file ? { file: r.file, line: r.line } : null)
        if (lastWord === word) pendingDef = r.file ? { word, file: r.file, line: r.line } : null
      }).catch(() => locateCache.set(word, null))
    }
    editor.onMouseMove((e) => {
      if (!ctrlDown || e.target.position == null) { if (lastWord) clearHover(); return }
      const w = editor.getModel()?.getWordAtPosition(e.target.position)
      if (!w) { if (lastWord) clearHover(); return }
      if (w.word !== lastWord) {
        lastWord = w.word
        pendingDef = null
        const ln = e.target.position.lineNumber
        fnDecos.set([{
          range: new monaco.Range(ln, w.startColumn, ln, w.endColumn),
          options: { inlineClassName: 'fn-link' },
        }])
        hoverLocate(w.word)
      }
    })
    editor.onMouseLeave(() => clearHover())
    editor.onMouseDown((e) => {
      // 内容区 Ctrl+左键:命中悬停预定位的词才跳转(行号/边栏点击不触发)
      if (!ctrlDown || e.target.type !== monaco.editor.MouseTargetType.CONTENT_TEXT || !pendingDef) return
      const w = editor.getModel()?.getWordAtPosition(e.target.position)
      if (w && w.word === pendingDef.word) {
        const d = pendingDef
        pendingDef = null
        void useStore.getState().openSourceTab(d.file, d.line)
      }
    })
  }

  // 装饰:断点圆点按文件归属过滤;停站/定位光标只画在归属文件上
  useEffect(() => {
    const ed = editorRef.current
    const decos = decosRef.current
    if (!ed || !decos) return
    const viewFile = isDebug ? sourceDVM : active!.file
    const list: monaco.editor.IModelDeltaDecoration[] = []
    for (const b of breakpoints) {
      // 断点归属过滤:跨文件跳转后,其它文件的断点行号不能画在当前文件上
      if (viewFile && b.file && progKey(b.file, module) !== progKey(viewFile, module)) continue
      list.push({
        range: new monaco.Range(b.line, 1, b.line, 1),
        options: {
          isWholeLine: true,
          glyphMarginClassName: 'bp-dot',
          overviewRuler: { color: '#ef4444', position: monaco.editor.OverviewRulerLane.Center },
        },
      })
    }
    if (cursorLine > 0 && (isDebug ? state === 'stopped' : true)) {
      list.push({
        range: new monaco.Range(cursorLine, 1, cursorLine, 1),
        options: {
          isWholeLine: true,
          className: isDebug ? 'cur-line-hl' : 'cur-line-hl',
          glyphMarginClassName: isDebug ? 'cur-arrow' : 'cur-arrow',
          overviewRuler: { color: '#eab308', position: monaco.editor.OverviewRulerLane.Center },
        },
      })
    }
    decos.set(list)
  }, [breakpoints, cursorLine, state, content, isDebug, sourceDVM, active?.file, active?.line, editorReady, modelTick, module])

  // 视口跟随(VS Code 式):高亮平移与滚动解耦——黄色高亮始终即时移动到新停站行,
  // 但视口只在「停站行不在视口内」时才居中(revealLineInCenterIfOutsideViewport),
  // 行还看得见就绝不翻页:单步时行向底边自然漂移,出界后才重新居中,无抖动。
  // 视口滚动唯一驱动 = revealReq 定位信号(停站落位/步进/跳函数/选帧/断点跳转);
  // 停站保持期间的快照刷新不发信号,手动滚走的位置也被尊重,直到下一次真停站。
  // 绝不能放进装饰 effect——它依赖 breakpoints,加断点重跑会把视口拽回运行行。
  // 纯页签切换不发信号:切回页签恢复上次离开的视口,阅读连续性不受光标位置影响。
  // 每个定位信号只消费一次(revealedSeqRef):content/state 变化引发的 effect 重跑不再重复滚动;
  // 内容未就绪(模型行数不够)时不消费,等 content 到位后重跑再滚——延迟 80ms 等换 model/setValue 完成
  const revealSeq = useStore((s) => s.revealReq?.seq ?? 0)
  const revealLine = useStore((s) => s.revealReq?.line ?? 0)
  const revealKey = useStore((s) => s.revealReq?.key ?? '')
  const revealNav = useStore((s) => s.revealReq?.nav ?? false) // 纯浏览跳转(断点列表):不停站也滚
  const revealedSeqRef = useRef(0)
  useEffect(() => {
    const targetKey = isDebug ? 'debug' : active?.key
    if (!(revealLine > 0) || revealKey !== targetKey) return
    if (isDebug && state !== 'stopped' && !revealNav) return
    if (revealSeq === revealedSeqRef.current) return
    const t = setTimeout(() => {
      const ed = editorRef.current
      if (!ed) return
      if ((ed.getModel()?.getLineCount() ?? 0) < revealLine) return
      revealedSeqRef.current = revealSeq
      ed.revealLineInCenterIfOutsideViewport(revealLine)
    }, 80)
    return () => clearTimeout(t)
  }, [revealSeq, revealLine, revealKey, revealNav, content, isDebug, state, active?.key])

  // 编辑器 options 必须稳定:字面量每次渲染都是新对象,会触发 @monaco-editor/react
  // 反复 updateOptions(minimap 重建),加断点等重渲染时会把滚动位置复位
  const editorOptions = useMemo(
    () => ({
      readOnly: true,
      glyphMargin: true,
      fontSize: 13,
      minimap: { enabled: true, renderCharacters: false, maxColumn: 120 },
      stickyScroll: { enabled: false }, // 关掉滚动时固定在顶部的函数/段头
      scrollBeyondLastLine: false,
      renderLineHighlight: 'none' as const,
      lineNumbersMinChars: 5,
      folding: false,
      // 对齐 BDL 的「只三种颜色」:下面两项在 Monaco 里的默认值都会引入第四种颜色 ——
      // 括号配色独立于 tokenizer,中文源码里的全角空格会被画框(BDL 侧用
      // configurationDefaults 关掉的是同样的两项)。
      bracketPairColorization: { enabled: false },
      unicodeHighlight: { ambiguousCharacters: false },
      automaticLayout: true,
      fixedOverflowWidgets: true, // 悬浮卡片越界时改挂 fixed 容器,避免被编辑器裁剪
      scrollbar: { verticalScrollbarSize: 10, horizontalScrollbarSize: 10 },
    }),
    [],
  )

  const busy = isDebug
    ? loadingSource // 启动/跨文件加载期间都显示转圈(启动时无内容也转,不闪空白)
    : !!active!.loading

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* 页签栏:调试页锁定第一个,浏览页可关 */}
      <div className="flex h-8 shrink-0 items-stretch overflow-x-auto border-b border-border bg-background">
        <button onClick={() => setActiveTab('debug')}
          className={cn('flex shrink-0 items-center gap-1.5 border-r border-border px-3 text-xs transition-colors',
            isDebug ? 'tab-active bg-card font-medium text-foreground' : 'text-muted-foreground hover:bg-accent/60')}>
          <Bug className="h-3 w-3" />
          {prog || '调试'}
        </button>
        {tabs.map((t) => (
          <div key={t.key}
            className={cn('group flex shrink-0 items-center border-r border-border transition-colors',
              activeTab === t.key ? 'tab-active bg-card text-foreground' : 'text-muted-foreground hover:bg-accent/60')}>
            <button className="max-w-45 truncate px-3 text-xs" title={t.path || t.file}
              onClick={() => setActiveTab(t.key)}>
              {t.loading ? <Loader2 className="mr-1 inline h-3 w-3 animate-spin" /> : null}
              {t.file}
              {t.missing ? ' (无源码)' : ''}
            </button>
            <button className="mr-1 p-0.5 opacity-40 transition-opacity hover:bg-accent hover:opacity-100"
              onClick={() => closeTab(t.key)}>
              <X className="h-3 w-3" />
            </button>
          </div>
        ))}
      </div>
      {/* 编辑器 */}
      <div className="relative min-h-0 flex-1">
        <Editor
          language="4gl"
          theme={monacoThemeName(theme)}
          path={modelPath}
          value={content}
          beforeMount={setupMonaco}
          onMount={onMount}
          options={editorOptions}
          loading={
            <div className="flex h-full items-center justify-center">
              <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            </div>
          }
        />
        {/* 调试页跨文件切换:旧文件保持显示但加遮罩,停站光标等源码到位再落位 */}
        {busy && (
          <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-2 bg-background/40">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            {/* 启动/重放期间说明在等什么(重放要先连服务器重读报文,秒级),别让人干看着转圈 */}
            {isDebug && state === 'loading' && (
              <div className="text-xs text-muted-foreground">
                {replayRowid ? '正在按接口日志重放,准备调试会话…' : '正在启动调试会话…'}
              </div>
            )}
          </div>
        )}
        {/* 调试页:停站且确无源码时提示(启动/加载期间只显示转圈,不打扰) */}
        {isDebug && !sourceContent && !busy && state === 'stopped' && stop?.file && (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
            <div className="border border-border bg-card/90 px-4 py-3 text-sm text-muted-foreground">
              该模块无源码(仅 42m),当前停站:<span className="text-foreground">{stop.file}:{stop.line}</span><br />
              <span className="text-xs">可继续用变量监视/调用栈分析,或继续运行回到有源码的模块</span>
            </div>
          </div>
        )}
        {!isDebug && active!.missing && (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
            <div className="border border-border bg-card/90 px-4 py-3 text-sm text-muted-foreground">
              该文件无源码(仅 42m 编译产物),无法静态浏览
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
