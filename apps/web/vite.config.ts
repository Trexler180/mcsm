import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'
import type { ServerResponse } from 'node:http'
import type { Socket } from 'node:net'

const apiPort = process.env.VITE_API_PORT ?? '8081'
const apiHost = process.env.VITE_API_HOST ?? '127.0.0.1'
const webPort = Number(process.env.PORT ?? '3000')

function writeProxyUnavailable(res: ServerResponse | Socket | undefined) {
  if (!res || res.destroyed) return

  if ('writeHead' in res && !res.headersSent) {
    res.writeHead(503, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ error: 'api unavailable' }))
    return
  }

  res.end()
}

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: webPort,
    proxy: {
      '/api': {
        target: `http://${apiHost}:${apiPort}`,
        changeOrigin: true,
        ws: true,
        configure: (proxy) => {
          proxy.on('error', (_err, _req, res) => {
            writeProxyUnavailable(res)
          })
        },
      },
    },
  },
  build: {
    rollupOptions: {
      output: {
        // Split heavy, independently-cacheable libraries into their own
        // chunks so a change in app code doesn't bust their cache and the
        // browser can fetch them in parallel. Build-only; no runtime change.
        manualChunks: {
          react: ['react', 'react-dom'],
          charts: ['recharts'],
          terminal: ['@xterm/xterm', '@xterm/addon-fit', '@xterm/addon-web-links'],
          editor: ['codemirror', '@codemirror/lang-json', '@codemirror/lang-yaml', '@codemirror/theme-one-dark'],
          markdown: ['react-markdown'],
        },
      },
    },
  },
})
