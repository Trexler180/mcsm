import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { VitePWA } from 'vite-plugin-pwa'
import path from 'path'
import { execSync } from 'node:child_process'
import type { ServerResponse } from 'node:http'
import type { Socket } from 'node:net'

// Build identity baked into the bundle as __APP_VERSION__: the git revision
// when building from a checkout, else "dev" (e.g. a source tarball).
function appVersion(): string {
  try {
    return execSync('git rev-parse --short=12 HEAD', { stdio: ['ignore', 'pipe', 'ignore'] })
      .toString()
      .trim() || 'dev'
  } catch {
    return 'dev'
  }
}

const apiPort = process.env.VITE_API_PORT ?? '8081'
const apiHost = process.env.VITE_API_HOST ?? '127.0.0.1'
const webPort = Number(process.env.PORT ?? '3000')

// Base path the app is served from. Defaults to root ('/') so local dev is
// unaffected; set VITE_BASE=/dashboard/ at build time to host under a subpath
// behind a reverse proxy. Must have a leading and trailing slash.
const base = process.env.VITE_BASE ?? '/'

// When set, emit a self-destroying service worker that unregisters any
// previously-installed SW and clears its caches. Useful for ephemeral/test
// deployments where stale PWA caches cause confusion. Off by default so normal
// production builds keep the full PWA behaviour.
const pwaSelfDestroy = process.env.VITE_PWA_SELF_DESTROY === '1'

// Full CSP for the built bundle, injected at build time only — the dev server
// needs inline scripts for HMR/react-refresh, so index.html ships a minimal
// baseline and this replaces it in `vite build` output. Notes:
//  - script-src 'self': the bundle has no inline scripts (SW registration is
//    in-bundle via virtual:pwa-register).
//  - style-src 'unsafe-inline': xterm.js and recharts inject <style> elements.
//  - img-src https:: player heads (mc-heads.net) and mod icons come from
//    third-party CDNs that vary by source.
//  - connect-src 'self' covers the same-origin API and its WebSockets.
//  - frame-ancestors is not valid in a <meta> policy; the proxy sets it (and
//    the API sets X-Frame-Options) — see docs/deployment.md.
const buildCSP = [
  "default-src 'self'",
  "script-src 'self'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: https:",
  "font-src 'self' data:",
  "connect-src 'self'",
  "worker-src 'self'",
  "manifest-src 'self'",
  "object-src 'none'",
  "base-uri 'self'",
  "form-action 'self'",
].join('; ')

const devCSP = "object-src 'none'; base-uri 'self'"

function buildCSPPlugin() {
  return {
    name: 'mcsm:build-csp',
    apply: 'build' as const,
    transformIndexHtml(html: string) {
      return html.replace(devCSP, buildCSP)
    },
  }
}

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
  base,
  plugins: [
    buildCSPPlugin(),
    react(),
    VitePWA({
      selfDestroying: pwaSelfDestroy,
      registerType: 'autoUpdate',
      includeAssets: ['favicon.svg', 'apple-touch-icon.png'],
      manifest: {
        name: 'MCSM',
        short_name: 'MCSM',
        description: 'Mod-aware operations panel for Minecraft servers',
        theme_color: '#0f0f0f',
        background_color: '#0f0f0f',
        display: 'standalone',
        // Scope/start_url follow the base path so the installed PWA and the
        // service worker are correctly bounded under a subpath deployment.
        start_url: base,
        scope: base,
        icons: [
          // Relative paths resolve against the manifest's own URL, so they
          // stay correct whether served from '/' or a subpath like '/dashboard/'.
          { src: 'pwa-192x192.png', sizes: '192x192', type: 'image/png' },
          { src: 'pwa-512x512.png', sizes: '512x512', type: 'image/png' },
          {
            src: 'pwa-maskable-512x512.png',
            sizes: '512x512',
            type: 'image/png',
            purpose: 'maskable',
          },
        ],
      },
      workbox: {
        globPatterns: ['**/*.{js,css,html,svg,png,woff2}'],
        // The API (including WebSockets) must always hit the network; only
        // the app shell and static assets are precached.
        navigateFallback: base + 'index.html',
        navigateFallbackDenylist: [/^\/api\//],
      },
    }),
  ],
  define: {
    __APP_VERSION__: JSON.stringify(appVersion()),
  },
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
      // Public status pages are served by the API at the origin root, so the
      // "open page" link from the options panel works in local dev too.
      '/status': {
        target: `http://${apiHost}:${apiPort}`,
        changeOrigin: true,
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
