import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: { chunkSizeWarningLimit: 9000 },
  server: {
    proxy: {
      '/api': { target: 'http://127.0.0.1:28670', ws: true },
      '/mcp': 'http://127.0.0.1:28670',
    },
  },
})
