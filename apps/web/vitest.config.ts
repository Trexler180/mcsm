import { defineConfig } from 'vitest/config'
import path from 'path'

// Test config lives in its own file so vite.config.ts (PWA manifest, base-path
// handling, build chunking — all deploy-sensitive) stays untouched by tooling.
export default defineConfig({
  define: {
    __APP_VERSION__: JSON.stringify('test'),
  },
  // The contract test (src/lib/contract.test.ts) reads the API's Go source to
  // hold the web app's copies of its names to the originals.
  server: {
    fs: { allow: [path.resolve(__dirname), path.resolve(__dirname, '../api')] },
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
    setupFiles: ['./src/test-setup.ts'],
  },
})
