import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// 后端地址:默认 127.0.0.1:28670(CLI `tdebug serve` 的默认监听)。
// 桌面版开发(desktop/dev.js)会通过 TDEBUG_PROXY 传入后端**真实**地址 ——
// 端口被占用顺延、或跑的是桌面模式(默认 28675)时,代理也能对上。
export default defineConfig(() => {
  const target = process.env.TDEBUG_PROXY || 'http://127.0.0.1:28670'
  return {
    plugins: [react(), tailwindcss()],
    build: { chunkSizeWarningLimit: 9000 },
    server: {
      proxy: {
        '/api': { target, ws: true },
        '/mcp': target,
      },
    },
  }
})
