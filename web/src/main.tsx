import React from 'react'
import ReactDOM from 'react-dom/client'
import { App } from './App'
import { editorRef } from './SourceView'
import { useStore } from './store'
import './index.css'

// 启动即同步主题类(store 默认 dark,但 setTheme 只在手动切换时挂 .dark,
// 首帧不挂会一直渲染亮色)——与 store.theme 的取值规则保持一致
document.documentElement.classList.toggle(
  'dark',
  localStorage.getItem('tdebug.theme') !== 'light',
)

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)

// 浏览器控制台调试入口
;(window as any).__ed = editorRef
;(window as any).__store = useStore
