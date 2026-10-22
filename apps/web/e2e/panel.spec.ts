// The panel end to end, against a real agent and a real Minecraft server: what
// was checked by hand before a release, made repeatable. The tests share one
// server and run in order; each leaves it as the next expects.
import { expect, test, type Page } from '@playwright/test'
import { existsSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { zipFolder } from './zip'

const SERVER = 'E2E Paper'
const admin = () => ({ email: process.env.E2E_ADMIN_EMAIL!, password: process.env.E2E_ADMIN_PASSWORD! })

async function signIn(page: Page, email: string, password: string) {
  await page.goto('/login')
  await page.locator('input[type=email]').fill(email)
  await page.locator('input[type=password]').fill(password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('navigation').first()).toBeVisible()
}

/** The state pill in the server header ("Stopped", "Running for 2m"). */
const state = (page: Page) => page.locator('.srv-facts > *').first()
const tab = (page: Page, name: string) => page.getByRole('tab', { name, exact: true })

/** A token for checking things through the API directly. */
async function token(email: string, password: string): Promise<string> {
  const r = await fetch(`${process.env.E2E_API}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  })
  expect(r.ok).toBe(true)
  return ((await r.json()) as { access_token: string }).access_token
}

test.describe.configure({ mode: 'serial' })

let page: Page
let serverId = ''

test.beforeAll(async ({ browser }) => {
  page = await browser.newPage()
  await signIn(page, admin().email, admin().password)
  // Picks up the server an earlier run left (E2E_REUSE), so one test can run alone.
  const r = await fetch(`${process.env.E2E_API}/api/v1/servers`, { headers: { Authorization: `Bearer ${await token(admin().email, admin().password)}` } })
  const list = (await r.json()) as { id: string; name: string }[]
  serverId = list.find((s) => s.name === SERVER)?.id ?? ''
})

/** Opens one of the server's tabs. */
async function open(tab = '', search = '') {
  await page.goto(`/s/${serverId}${tab ? '/' + tab : ''}${search}`)
}

test.afterAll(async () => {
  await page.close()
})

test('creates a server', async () => {
  test.skip(serverId !== '', 'left by an earlier run')
  await page.getByRole('button', { name: 'Add a server' }).first().click()
  await page.getByRole('menuitem', { name: 'Add a server' }).click()
  await page.getByRole('textbox', { name: 'Name' }).fill(SERVER)
  // Clear of any server a developer has running.
  await page.getByRole('textbox', { name: 'Port' }).fill('25599')
  // The newest version fills in once the list arrives.
  await expect(page.getByRole('button', { name: 'Minecraft version' })).toHaveText(/\d/)
  // One version behind the newest, so a later test can move it up.
  await page.getByRole('button', { name: 'Minecraft version' }).click()
  await page.getByRole('option').nth(1).click()
  await page.getByRole('button', { name: 'Create server' }).click()
  await expect(page.getByRole('heading', { level: 1, name: SERVER })).toBeVisible()
  serverId = page.url().split('/s/')[1].split(/[/?]/)[0]
  expect(serverId).not.toBe('')
  await expect(state(page)).toHaveText(/Stopped/)
})

test('starts, takes a command, and stops', async () => {
  await open()
  await page.getByRole('button', { name: 'Start', exact: true }).click()
  // The first start downloads the server jar and builds a world.
  await expect(state(page)).toHaveText(/Starting/, { timeout: 30_000 })
  await expect(state(page)).toHaveText(/Running/, { timeout: 5 * 60_000 })

  const command = page.getByRole('textbox', { name: 'Command' })
  await command.fill('list')
  await command.press('Enter')
  await expect(page.getByRole('log')).toContainText('There are 0 of a max', { timeout: 30_000 })

  await page.getByRole('button', { name: 'Stop', exact: true }).click()
  await expect(state(page)).toHaveText(/Stopped/, { timeout: 90_000 })
})

test('saves a setting and leaves nothing unsaved', async () => {
  await open('settings')
  const motd = page.getByRole('textbox', { name: 'MOTD' })
  await motd.fill('Changed by the e2e suite')
  await expect(page.getByText(/1 unsaved change/)).toBeVisible()
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Saved', { exact: true }).first()).toBeVisible()
  // The bar used to stay up after every save on a server with no resource pack.
  await expect(page.getByText(/unsaved change/)).toHaveCount(0, { timeout: 10_000 })
})

test('backs up, then restores to before the setting changed', async () => {
  // The backup holds the changed MOTD; change it again, then restore.
  await open('backups')
  await page.getByRole('button', { name: 'Back up now' }).click()
  await expect(page.locator('main tbody tr').first()).toContainText(/[KMG]B/, { timeout: 2 * 60_000 })

  await tab(page, 'Settings').click()
  await page.getByRole('textbox', { name: 'MOTD' }).fill('Changed after the backup')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText(/unsaved change/)).toHaveCount(0, { timeout: 10_000 })

  await tab(page, 'Backups').click()
  await page.locator('main tbody tr').filter({ hasText: 'Made by hand' }).first().getByRole('button', { name: 'Restore' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Restore' }).click()
  await expect(page.getByText(`Restored ${SERVER}`)).toBeVisible({ timeout: 3 * 60_000 })

  await tab(page, 'Settings').click()
  await expect(page.getByRole('textbox', { name: 'MOTD' })).toHaveValue('Changed by the e2e suite')
})

test('creates, edits, and deletes a file', async () => {
  await open('files')
  await page.getByRole('button', { name: 'New', exact: true }).click()
  await page.getByRole('menuitem', { name: 'New file' }).click()
  await page.getByRole('dialog').getByRole('textbox', { name: 'Name' }).fill('e2e.txt')
  await page.getByRole('dialog').getByRole('button', { name: 'Create' }).click()

  const editor = page.locator('.cm-content')
  await editor.click()
  await editor.pressSequentially('written by the e2e suite')
  await page.keyboard.press('Control+s')

  const t = await token(admin().email, admin().password)
  await expect
    .poll(async () => {
      const r = await fetch(`${process.env.E2E_API}/api/v1/servers/${serverId}/files/content?path=%2Fe2e.txt`, { headers: { Authorization: `Bearer ${t}` } })
      return r.ok ? r.text() : ''
    })
    .toBe('written by the e2e suite')

  await page.getByRole('button', { name: 'Close' }).click()
  await page.getByRole('button', { name: 'More for e2e.txt' }).click()
  await page.getByRole('menuitem', { name: 'Delete' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByRole('button', { name: 'More for e2e.txt' })).toHaveCount(0)
})

test('schedules a backup without naming it, runs it, and deletes it', async () => {
  await open('schedules')
  await page.getByRole('button', { name: 'New schedule' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: 'What to do' }).click()
  await page.getByRole('option', { name: 'Back it up' }).click()
  // A blank name takes the suggestion the field shows.
  await expect(dialog.getByRole('textbox', { name: 'Name' })).toHaveAttribute('placeholder', 'Nightly backup')
  await dialog.getByRole('button', { name: 'Schedule it' }).click()
  await expect(page.getByText('Nightly backup', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'Run now' }).click()
  await page.getByRole('button', { name: 'History' }).click({ timeout: 60_000 })
  await expect(page.getByRole('cell', { name: 'Done' })).toBeVisible({ timeout: 2 * 60_000 })

  await page.getByRole('button', { name: 'More for Nightly backup' }).click()
  await page.getByRole('menuitem', { name: 'Delete' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText('Nothing is scheduled.')).toBeVisible()
})

test('uploads a world as a second world', async () => {
  // The server's own world, zipped as someone would from their game folder.
  const folder = join(process.env.E2E_SERVERS!, 'e2e-paper')
  const zip = zipFolder(join(folder, 'world'), 'My World', (rel) => rel === 'session.lock')
  await open('worlds')
  await page.getByRole('button', { name: 'Upload a world' }).click()
  const dialog = page.getByRole('dialog', { name: 'Upload a world' })
  await dialog.locator('input[type=file]').setInputFiles({ name: 'My World.zip', mimeType: 'application/zip', buffer: zip })
  await expect(dialog.getByRole('textbox', { name: 'Folder name' })).toHaveValue(/My.World/)
  await dialog.getByRole('textbox', { name: 'Folder name' }).fill('e2e-uploaded')
  await dialog.getByRole('button', { name: 'Upload', exact: true }).click()
  await expect(page.getByText('Uploaded e2e-uploaded')).toBeVisible({ timeout: 60_000 })
  await expect(page.getByRole('main').getByText('e2e-uploaded').first()).toBeVisible()
  expect(existsSync(join(folder, 'e2e-uploaded', 'level.dat'))).toBe(true)
  // Nothing of the upload is left beside the server folder.
  expect(readdirSync(process.env.E2E_SERVERS!).filter((n) => n.includes('world-new') || n.includes('world-old'))).toEqual([])
})

test('installs a plugin from Hangar', async () => {
  await open('mods', '?sub=browse')
  await page.getByRole('textbox', { name: 'Search Hangar' }).fill('Chunky')
  const row = page.locator('.item', { has: page.getByRole('button', { name: 'Chunky', exact: true }) })
  await row.getByRole('button', { name: 'Install' }).click()
  await expect(row).toContainText('Installed', { timeout: 60_000 })
  // Once, even after the panel has reconciled its list with the folder (a
  // rescan racing the install used to add a second row for the same jar).
  const t = await token(admin().email, admin().password)
  const r = await fetch(`${process.env.E2E_API}/api/v1/servers/${serverId}/mods/rescan`, { method: 'POST', headers: { Authorization: `Bearer ${t}` } })
  expect(r.ok).toBe(true)
  await open('mods')
  await expect(page.getByRole('table').getByText('Chunky', { exact: true })).toHaveCount(1)
})

/** Checks the server against the newest version and moves it there. */
async function moveToNewest() {
  await page.getByRole('button', { name: 'Target version' }).click()
  await page.getByRole('option').first().click()
  await page.getByRole('button', { name: 'Check', exact: true }).click()
  await expect(page.getByRole('row', { name: /Chunky/ })).toContainText('Works as it is')
  await page.getByRole('button', { name: /^Move to / }).click()
  await page.getByRole('dialog').getByRole('button', { name: /^Move to / }).click()
}

/** The version page's Java flags, saved. */
async function setJavaFlags(flags: string) {
  await page.getByRole('main').getByRole('textbox').last().fill(flags)
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText(/unsaved change/)).toHaveCount(0, { timeout: 10_000 })
}

test('puts the backup back when the new version doesn’t start', async () => {
  await open('settings', '?sub=version')
  const from = (await page.getByRole('button', { name: 'Minecraft version' }).textContent())!.trim()
  // A flag Java refuses, so the trial start on the new version fails.
  await setJavaFlags('-XX:+NoSuchOptionForTheE2ESuite')
  await moveToNewest()
  await expect(page.getByRole('main').getByText(/it didn’t start, so the backup was put back/)).toBeVisible({ timeout: 5 * 60_000 })
  await expect(page.getByRole('button', { name: 'Minecraft version' })).toHaveText(from)
  await setJavaFlags('')

  // People hear about it, and the alert leads back to the version page.
  await page.goto('/alerts')
  const alert = page.locator('.item', { hasText: `${SERVER} didn't move to` })
  await expect(alert).toContainText(`The server was put back on ${from}`)
  await alert.getByRole('button', { name: 'Version' }).click()
  await expect(page).toHaveURL(/\/settings\?sub=version/)
})

test('moves to the newest Minecraft version', async () => {
  await open('settings', '?sub=version')
  const from = (await page.getByRole('button', { name: 'Minecraft version' }).textContent())!.trim()
  await moveToNewest()
  await expect(page.getByRole('main').getByText(new RegExp(`Moved from ${from} to `))).toBeVisible({ timeout: 5 * 60_000 })
  await expect(page.getByRole('button', { name: 'Minecraft version' })).not.toHaveText(from)
  await expect(state(page)).toHaveText(/Stopped/)
})

test('a view-only member sees only their server and cannot start it', async ({ browser }) => {
  const email = 'e2e-member@localhost.test'
  await page.getByRole('navigation').getByRole('button', { name: 'People' }).click()
  await page.getByRole('button', { name: 'Add someone' }).click()
  const add = page.getByRole('dialog')
  await add.getByRole('textbox', { name: 'Email' }).fill(email)
  const password = await add.locator('input[aria-label=Password]').inputValue()
  await add.getByRole('button', { name: 'Add', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Done' }).click()

  await open('settings', '?sub=access')
  await page.getByRole('main').getByRole('button', { name: 'Add someone' }).last().click()
  const access = page.getByRole('dialog')
  await access.locator('input').first().fill(email)
  await access.getByRole('button', { name: 'Can only look' }).click()
  await access.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(page.getByRole('main')).toContainText(email)

  const member = await browser.newPage()
  try {
    await signIn(member, email, password)
    const servers = member.getByRole('navigation').getByRole('button', { name: SERVER })
    await expect(servers).toHaveCount(1)
    await member.goto(`/s/${serverId}/stats`)
    await expect(member.getByRole('button', { name: 'Start', exact: true })).toBeDisabled()

    const t = await token(email, password)
    const r = await fetch(`${process.env.E2E_API}/api/v1/servers/${serverId}/start`, { method: 'POST', headers: { Authorization: `Bearer ${t}` } })
    expect(r.status).toBe(403)
  } finally {
    await member.close()
  }
})

test('deletes the server and its folder', async () => {
  await open('settings')
  await page.getByRole('main').getByRole('button', { name: 'Delete', exact: true }).click()
  const dialog = page.getByRole('dialog')
  for (const box of await dialog.getByRole('checkbox').all()) await box.check()
  await dialog.locator('input:not([type=checkbox])').fill(SERVER)
  await dialog.getByRole('button', { name: 'Delete', exact: true }).click()
  await page.getByRole('button', { name: 'Delete them' }).click()
  await expect(page.getByText(`Deleted ${SERVER}`, { exact: true })).toBeVisible()
  await expect(page.getByRole('navigation').getByRole('button', { name: SERVER })).toHaveCount(0)
  await expect.poll(() => existsSync(join(process.env.E2E_SERVERS!, 'e2e-paper'))).toBe(false)
})
