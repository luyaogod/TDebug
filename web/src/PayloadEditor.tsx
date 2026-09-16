// 报文编辑器/查看器:用 Monaco 承载 request / response 文本。
// 与源码编辑器共用同一套主题(monacoSetup 里的 tdebug-dark/light),
// 因此背景色、字体、滚动条观感与主编辑器完全一致。
// 只读模式用于日志明细的 Request/Response 页;可编辑模式用于服务测试的请求报文。
import Editor from '@monaco-editor/react'
import { useStore } from './store'
import { setupMonaco, monacoThemeName } from './monacoSetup'

/** 报文语言:JSON 交给 monaco 的 json(高亮 + 校验),其余按纯文本 */
function payloadLanguage(text: string): string {
  const t = text.trimStart()
  return t.startsWith('{') || t.startsWith('[') ? 'json' : 'plaintext'
}

export function PayloadEditor({ text, editable = false, onChange, loading, emptyHint, path }: {
  text?: string
  editable?: boolean
  onChange?: (v: string) => void
  loading?: boolean
  emptyHint?: string
  /** 模型路径:同一路径复用同一 model(切页签不丢视口/undo);只读查看器可不传 */
  path?: string
}) {
  const dark = useStore((s) => s.dark)
  const value = text ?? ''

  // 外层统一铺编辑器背景色(--card 在两套主题里都等于 editor.background),
  // 这样"加载中/无报文"占位态与 Monaco 本身的背景一致,不会一块深一块浅。
  if (loading) return <div className="h-full w-full bg-card p-3 text-xs text-muted-foreground">加载中…</div>
  // 只读且无内容时不给编辑器(空 Monaco 只剩背景,反而更乱)
  if (!editable && !value) {
    return <div className="h-full w-full bg-card p-3 text-xs text-muted-foreground">{emptyHint || '无内容'}</div>
  }

  return (
    <div className="h-full w-full bg-card">
      <Editor
        path={path}
        language={payloadLanguage(value)}
        theme={monacoThemeName(dark)}
        value={value}
        beforeMount={setupMonaco}
        onChange={(v) => onChange?.(v ?? '')}
        options={{
          readOnly: !editable,
          domReadOnly: !editable,
          minimap: { enabled: false },
          lineNumbers: 'on',
          folding: false,
          wordWrap: 'on',
          scrollBeyondLastLine: false,
          renderLineHighlight: 'none',
          fontSize: 12,
          // 紧凑一些:与右侧面板的行高观感接近
          lineHeight: 18,
          scrollbar: { verticalScrollbarSize: 10, horizontalScrollbarSize: 10 },
          automaticLayout: true,
          // 关掉"吸顶的当前作用域"浮层(JSON 会按层级顶在编辑器上方,看起来像悬浮多余物);
          // 与 SourceView 的选项保持一致
          stickyScroll: { enabled: false },
          // 只读查看时仍允许选中/复制,但不给编辑相关提示
          contextmenu: editable,
          occurrencesHighlight: 'off',
          renderWhitespace: 'none',
          padding: { top: 6, bottom: 6 },
        }}
      />
    </div>
  )
}
