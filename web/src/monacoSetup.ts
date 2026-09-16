// Monaco 初始化:语言注册、主题、worker 路由。由 SourceView 与 PayloadEditor 共用。
// 必须在任何 <Editor> 挂载前执行(SourceView 在模块加载时调用;PayloadEditor 再兜一层),
// 否则 loader 可能走 CDN、主题/语言状态不一致。
import { loader } from '@monaco-editor/react'
import * as monaco from 'monaco-editor'
import editorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'
import jsonWorker from 'monaco-editor/esm/vs/language/json/json.worker?worker'
// codicon 图标映射表:ESM 用法要求宿主显式引入,否则 Ctrl+F 查找控件等只有空按钮
import 'monaco-editor/esm/vs/base/browser/ui/codicons/codiconStyles.js'
import { FGL_MONARCH } from './fglTokens'

let registered = false

export function setupMonaco() {
  if (registered) return
  registered = true
  // 按 language label 路由 worker:JSON 报文编辑器要真正的 json worker
  // (校验/补全在 worker 里跑;此前所有 label 都返回 editor worker,一旦有 json 模型会报协议错)
  self.MonacoEnvironment = {
    getWorker: (_id: string, label: string) => (label === 'json' ? new jsonWorker() : new editorWorker()),
  }
  loader.config({ monaco })
  monaco.languages.register({ id: '4gl' })
  // 4GL 高亮定义移植自 BDL 扩展(见 fglTokens.ts):只发 comment/string/keyword 三类 token
  monaco.languages.setMonarchTokensProvider('4gl', FGL_MONARCH)
  monaco.editor.defineTheme('tdebug-dark', {
    base: 'vs-dark',
    inherit: true,
    // 语法配色对齐 VS Code Dark Modern 默认主题;只列三类,与 tokenizer 一一对应
    // (数字/标识符/括号改为继承主题默认前景色,类型关键字并入 keyword 蓝)
    rules: [
      { token: 'keyword', foreground: '569cd6' },
      { token: 'comment', foreground: '6a9955' },
      { token: 'string', foreground: 'ce9178' },
    ],
    colors: {
      'editor.background': '#1f1f1f',
      // 滚动条适配暗色(VS Code 半透明方形滑块)
      'scrollbarSlider.background': '#79797966',
      'scrollbarSlider.hoverBackground': '#797979b3',
      'scrollbarSlider.activeBackground': '#797979b3',
    },
  })
  monaco.editor.defineTheme('tdebug-light', {
    base: 'vs',
    inherit: true,
    // 语法配色对齐 VS Code Light Modern 默认主题
    rules: [
      { token: 'keyword', foreground: '0000ff' },
      { token: 'comment', foreground: '008000' },
      { token: 'string', foreground: 'a31515' },
    ],
    colors: {
      'editor.background': '#ffffff',
      'scrollbarSlider.background': '#64646466',
      'scrollbarSlider.hoverBackground': '#646464b3',
      'scrollbarSlider.activeBackground': '#646464b3',
    },
  })
}

/** 当前主题名(报文编辑器与源码编辑器用同一套主题 = 同一背景色)。
 *  入参是"解析后的明暗"(store.dark):这样"跟随系统"时系统一变化编辑器也跟着换,
 *  不会停在旧配色上。 */
export function monacoThemeName(dark: boolean): string {
  return dark ? 'tdebug-dark' : 'tdebug-light'
}
