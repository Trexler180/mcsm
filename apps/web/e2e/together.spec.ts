// Two people on one server at once: each sees what the other does without
// reloading, and neither's edit is lost to the other's save.
import { expect, test, type Browser, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

const SERVER = 'E2E Together'
let serverId = ''
let serverDir = ''
let a: Page
let b: Page

async function adminToken(): Promise<string> {
  const r = await fetch(`${process.env.E2E_API}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: process.env.E2E_ADMIN_EMAIL, password: process.env.E2E_ADMIN_PASSWORD }),
  })
  return ((await r.json()) as { access_token: string }).access_token
}

async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch(`${process.env.E2E_API}/api/v1${path}`, {
    method,
    headers: { Authorization: `Bearer ${await adminToken()}`, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  expect(r.ok, `${method} ${path}: ${r.status}`).toBe(true)
  return (r.status === 204 ? undefined : await r.json()) as T
}

/** A signed-in admin in a browser context of its own, like a second person. */
async function person(browser: Browser): Promise<Page> {
  const page = await (await browser.newContext()).newPage()
  await page.goto('/login')
  await page.locator('input[type=email]').fill(process.env.E2E_ADMIN_EMAIL!)
  await page.locator('input[type=password]').fill(process.env.E2E_ADMIN_PASSWORD!)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('navigation').first()).toBeVisible()
  return page
}

const state = (page: Page) => page.locator('.srv-facts > *').first()
const property = (key: string) =>
  readFileSync(join(serverDir, 'server.properties'), 'utf-8')
    .split(/\r?\n/)
    .find((l) => l.startsWith(key + '='))
    ?.slice(key.length + 1)

test.describe.configure({ mode: 'serial' })

test.beforeAll(async ({ browser }) => {
  const node = (await api<{ id: string }[]>('GET', '/nodes'))[0]
  const versions = await api<{ version: string; stable: boolean }[]>('GET', '/minecraft/versions?platform=paper')
  ;({ id: serverId, directory_path: serverDir } = await api<{ id: string; directory_path: string }>('POST', '/servers', {
      name: SERVER,
      node_id: node.id,
      platform: 'paper',
      mc_version: versions.find((v) => v.stable)!.version,
      port: 25595,
      ram_mb_max: 2048,
    }))
  a = await person(browser)
  b = await person(browser)
})

test.afterAll(async () => {
  await a.context().close()
  await b.context().close()
  await fetch(`${process.env.E2E_API}/api/v1/servers/${serverId}/kill?force=true`, { method: 'POST', headers: { Authorization: `Bearer ${await adminToken()}` } })
  await api('DELETE', `/servers/${serverId}?files=true&backups=true`)
})

test('two people editing different properties both keep their change', async () => {
  await a.goto(`/s/${serverId}/settings?sub=properties`)
  await b.goto(`/s/${serverId}/settings?sub=properties`)

  // B starts an edit and hasn't saved when A saves a different one.
  await b.getByRole('spinbutton', { name: 'View Distance' }).or(b.getByRole('textbox', { name: 'View Distance' })).fill('7')
  const maxA = a.getByRole('spinbutton', { name: 'Max Players' }).or(a.getByRole('textbox', { name: 'Max Players' }))
  await maxA.fill('33')
  await a.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(a.getByText(/unsaved change/)).toHaveCount(0, { timeout: 10_000 })
  await expect.poll(() => property('max-players')).toBe('33')

  // B's edit is still there, and saving it keeps A's.
  const viewB = b.getByRole('spinbutton', { name: 'View Distance' }).or(b.getByRole('textbox', { name: 'View Distance' }))
  await expect(viewB).toHaveValue('7')
  await b.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(b.getByText(/unsaved change/)).toHaveCount(0, { timeout: 10_000 })
  await expect.poll(() => property('view-distance')).toBe('7')
  expect(property('max-players')).toBe('33')
})

test('the file editor asks before saving over a newer copy', async () => {
  await a.goto(`/s/${serverId}/files?open=${encodeURIComponent('/server.properties')}`)
  const editor = a.locator('.cm-content')
  await expect(editor).toContainText('max-players=33')
  // B saves a change through the form while A has the file open.
  await b.goto(`/s/${serverId}/settings?sub=properties`)
  await b.getByRole('textbox', { name: 'Max Players' }).fill('44')
  await b.getByRole('button', { name: 'Save', exact: true }).click()
  await expect.poll(() => property('max-players')).toBe('44')

  await editor.click()
  await a.keyboard.press('Control+End')
  await a.keyboard.press('Enter')
  await a.keyboard.type('# a note from A')
  await a.keyboard.press('Control+s')
  const asked = a.getByRole('dialog', { name: 'server.properties changed since you opened it' })
  await expect(asked).toBeVisible()
  await asked.getByRole('button', { name: 'Cancel' }).click()
  expect(property('max-players')).toBe('44')
})

test('a start in one tab shows in the other without reloading', async () => {
  await a.goto(`/s/${serverId}`)
  await b.goto(`/s/${serverId}`)
  await expect(state(b)).toHaveText(/Stopped/)

  await a.getByRole('button', { name: 'Start', exact: true }).click()
  await expect(state(b)).toHaveText(/Starting/, { timeout: 30_000 })
  await expect(state(b)).toHaveText(/Running/, { timeout: 5 * 60_000 })
  await expect(b.getByRole('log')).toContainText('Done', { timeout: 30_000 })

  await b.getByRole('button', { name: 'Stop', exact: true }).click()
  await expect(state(a)).toHaveText(/Stopped/, { timeout: 90_000 })
})
