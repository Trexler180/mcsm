// A long run, skipped unless E2E_SOAK_MINUTES is set: dashboards stay open
// while a server is started, backed up, restarted and stopped over and over,
// and the API and agent are watched for memory that only grows, server
// errors, and pages that throw.
//
//   E2E_SOAK_MINUTES=20 pnpm e2e soak
import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { PORTS } from './stack'

const minutes = Number(process.env.E2E_SOAK_MINUTES ?? 0)
test.skip(!minutes, 'set E2E_SOAK_MINUTES to run the soak')

async function adminToken(): Promise<string> {
  const r = await fetch(`${process.env.E2E_API}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: process.env.E2E_ADMIN_EMAIL, password: process.env.E2E_ADMIN_PASSWORD }),
  })
  return ((await r.json()) as { access_token: string }).access_token
}

let token = ''
async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const call = () =>
    fetch(`${process.env.E2E_API}/api/v1${path}`, {
      method,
      headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  let r = await call()
  if (r.status === 401) {
    token = await adminToken()
    r = await call()
  }
  expect(r.ok, `${method} ${path}: ${r.status} ${r.ok ? '' : await r.clone().text()}`).toBe(true)
  return (r.status === 204 ? undefined : await r.json()) as T
}

const status = async (id: string) => (await api<{ status: string }>('GET', `/servers/${id}`)).status
async function until(id: string, want: RegExp, ms: number) {
  await expect.poll(() => status(id), { timeout: ms, intervals: [1000, 2000, 3000] }).toMatch(want)
}

/** Resident memory of whatever listens on port, in MB (Windows and Linux). */
function memoryMB(port: number): number {
  if (process.platform === 'win32') {
    const out = execFileSync('powershell', [
      '-NoProfile',
      '-Command',
      `(Get-Process -Id (Get-NetTCPConnection -LocalPort ${port} -State Listen).OwningProcess).WorkingSet64`,
    ])
      .toString()
      .trim()
    return Math.round(Number(out.split(/\s+/)[0]) / 1048576)
  }
  const pid = execFileSync('sh', ['-c', `ss -ltnpH 'sport = :${port}' | grep -o 'pid=[0-9]*' | head -1 | cut -d= -f2`]).toString().trim()
  return Math.round(Number(/VmRSS:\s+(\d+)/.exec(readFileSync(`/proc/${pid}/status`, 'utf-8'))![1]) / 1024)
}

test('dashboards stay up and memory levels off while a server cycles', async ({ browser }) => {
  test.setTimeout((minutes + 15) * 60_000)
  token = await adminToken()
  const node = (await api<{ id: string }[]>('GET', '/nodes'))[0]
  const versions = await api<{ version: string; stable: boolean }[]>('GET', '/minecraft/versions?platform=paper')
  const srv = await api<{ id: string }>('POST', '/servers', {
    name: 'E2E Soak',
    node_id: node.id,
    platform: 'paper',
    mc_version: versions.find((v) => v.stable)!.version,
    port: 25594,
    ram_mb_max: 2048,
  })

  // Four people looking at different things the whole time.
  const errors: string[] = []
  const pages: Page[] = []
  for (const path of ['/', `/s/${srv.id}`, `/s/${srv.id}/stats`, `/s/${srv.id}/backups`]) {
    const page = await (await browser.newContext()).newPage()
    page.on('pageerror', (e) => errors.push(`${path}: ${e.message}`))
    await page.goto('/login')
    await page.locator('input[type=email]').fill(process.env.E2E_ADMIN_EMAIL!)
    await page.locator('input[type=password]').fill(process.env.E2E_ADMIN_PASSWORD!)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('navigation').first()).toBeVisible()
    await page.goto(path)
    pages.push(page)
  }

  const samples: { at: number; api: number; agent: number }[] = []
  const sample = () => samples.push({ at: Date.now(), api: memoryMB(PORTS.api), agent: memoryMB(PORTS.agent) })
  const deadline = Date.now() + minutes * 60_000
  let cycles = 0
  try {
    while (Date.now() < deadline) {
      await api('POST', `/servers/${srv.id}/start`)
      await until(srv.id, /^online$/, 5 * 60_000)
      await api('POST', `/servers/${srv.id}/command`, { command: 'list' })
      await new Promise((r) => setTimeout(r, 30_000))
      const b = await api<{ operation: { id: string } | null }>('POST', `/servers/${srv.id}/backups`)
      if (b.operation)
        await expect
          .poll(async () => (await api<{ status: string }>('GET', `/servers/${srv.id}/operations/${b.operation!.id}`)).status, { timeout: 5 * 60_000 })
          .toBe('succeeded')
      await api('POST', `/servers/${srv.id}/restart`)
      await until(srv.id, /^online$/, 5 * 60_000)
      await api('POST', `/servers/${srv.id}/stop`)
      await until(srv.id, /^offline$/, 2 * 60_000)
      cycles++
      sample()
    }

    // The live pages still follow the server: the console page sees a start.
    await api('POST', `/servers/${srv.id}/start`)
    await expect(pages[1].locator('.srv-facts > *').first()).toHaveText(/Running/, { timeout: 5 * 60_000 })
  } finally {
    for (const p of pages) await p.context().close()
    await fetch(`${process.env.E2E_API}/api/v1/servers/${srv.id}/kill?force=true`, { method: 'POST', headers: { Authorization: `Bearer ${token}` } })
    await api('DELETE', `/servers/${srv.id}?files=true&backups=true`)
  }

  console.log(`soak: ${cycles} cycles in ${minutes} min`)
  console.log(samples.map((s) => `  +${Math.round((s.at - samples[0].at) / 60_000)}m api ${s.api} MB, agent ${s.agent} MB`).join('\n'))

  expect(errors, 'errors thrown in the open pages').toEqual([])
  const fivexx = readFileSync(join(process.env.E2E_DIR!, 'logs', 'api.log'), 'utf-8')
    .split('\n')
    .filter((l) => / status=5\d\d /.test(l))
  expect(fivexx, 'server errors in the API log').toEqual([])
  expect(cycles).toBeGreaterThan(1)
  // After the first cycles warm things up, memory should level off rather than
  // climb with every cycle.
  const settled = samples[Math.min(2, samples.length - 1)]
  const last = samples[samples.length - 1]
  expect(last.api, 'API memory').toBeLessThan(settled.api * 1.5 + 30)
  expect(last.agent, 'agent memory').toBeLessThan(settled.agent * 1.5 + 30)
})
