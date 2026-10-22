import { defineConfig, devices } from '@playwright/test'

// End-to-end suite: drives a real panel, agent and Minecraft server, brought up
// fresh by e2e/stack.ts. Slow (the first start downloads a server jar) and
// needs Go, Java and network access, so it runs on demand, not with the unit
// tests: `pnpm e2e`. E2E_KEEP=1 keeps the stack's folder and logs afterwards;
// E2E_REUSE=1 keeps the whole stack running for the next run.
export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/stack.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 6 * 60_000,
  expect: { timeout: 15_000 },
  reporter: [['list']],
  use: {
    ...devices['Desktop Chrome'],
    baseURL: 'http://127.0.0.1:13000',
    viewport: { width: 1280, height: 800 },
    trace: 'retain-on-failure',
    actionTimeout: 20_000,
    navigationTimeout: 30_000,
  },
})
