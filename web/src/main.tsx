import React from 'react'
import ReactDOM from 'react-dom/client'
import { App } from './App'
import { editorRef } from './SourceView'
import { useStore } from './store'
import { applyDark, readStoredTheme, resolveDark } from './theme'
import './index.css'

// 启动即同步主题类(store 默认暗色,但只有手动切换时才挂 .dark,首帧不挂会闪一下亮色)——
// 与 store 的取值规则保持一致;index.html 里的内联脚本负责更早的一帧(避免首屏闪色)。
applyDark(resolveDark(readStoredTheme()))

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)

// 浏览器控制台调试入口
;(window as any).__ed = editorRef
;(window as any).__store = useStore
