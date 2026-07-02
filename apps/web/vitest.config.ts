import { defineConfig } from 'vitest/config'
import path from 'path'

// Test config lives in its own file so vite.config.ts (PWA manifest, base-path
// handling, build chunking — all deploy-sensitive) stays untouched by tooling.
export default defineConfig({
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
