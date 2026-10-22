// Two-factor sign-in from a person's side: turning it on with an
// authenticator, signing in with a code, with a recovery code (once), making
// new codes, and turning it off.
import { expect, test, type Page } from '@playwright/test'
import { createHmac, randomBytes } from 'node:crypto'

const EMAIL = `e2e-2fa-${randomBytes(3).toString('hex')}@localhost.test`
const PASSWORD = randomBytes(12).toString('hex')

/** RFC 6238: the six-digit code an authenticator shows for a base32 secret. */
function totp(secret: string, at = Date.now()): string {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = ''
  for (const ch of secret.replace(/=+$/, '').toUpperCase()) bits += alphabet.indexOf(ch).toString(2).padStart(5, '0')
  const key = Buffer.from(bits.match(/.{8}/g)!.map((b) => parseInt(b, 2)))
  const counter = Buffer.alloc(8)
  counter.writeBigUInt64BE(BigInt(Math.floor(at / 30_000)))
  const mac = createHmac('sha1', key).update(counter).digest()
  const o = mac[mac.length - 1] & 0xf
  return String((mac.readUInt32BE(o) & 0x7fffffff) % 1_000_000).padStart(6, '0')
}

/** A code from a time step the server hasn't seen this user use yet. */
let lastStep = 0
async function freshCode(secret: string): Promise<string> {
  for (;;) {
    const step = Math.floor(Date.now() / 30_000)
    if (step > lastStep) {
      lastStep = step
      return totp(secret)
    }
    await new Promise((r) => setTimeout(r, 1000))
  }
}

async function signIn(page: Page, step: 'password' | 'code' | 'recovery', code = '') {
  await page.goto('/login')
  await page.locator('input[type=email]').fill(EMAIL)
  await page.locator('input[type=password]').fill(PASSWORD)
  await page.getByRole('button', { name: 'Sign in' }).click()
  if (step === 'password') return
  if (step === 'recovery') {
    await page.getByRole('button', { name: /Use a recovery code/ }).click()
    await page.getByRole('textbox', { name: 'Recovery code' }).fill(code)
  } else {
    await page.getByRole('textbox', { name: 'Two-factor code' }).fill(code)
  }
  await page.getByRole('button', { name: 'Continue' }).click()
}

/** The codes a "Save your recovery codes" dialog shows, once it has them. */
async function shownCodes(page: Page): Promise<string[]> {
  const codes = page.getByRole('dialog', { name: 'Save your recovery codes' }).locator('.recovery-codes span')
  await expect(codes.first()).toHaveText(/^\w{4}-\w{4}/)
  return (await codes.allTextContents()).map((c) => c.trim()).filter((c) => /^\w{4}(-\w{4})+$/.test(c))
}

const signedIn = (page: Page) => expect(page.getByRole('navigation').first()).toBeVisible()

test.describe.configure({ mode: 'serial' })

let secret = ''
let recovery: string[] = []

test.beforeAll(async () => {
  const login = await fetch(`${process.env.E2E_API}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: process.env.E2E_ADMIN_EMAIL, password: process.env.E2E_ADMIN_PASSWORD }),
  })
  const { access_token } = (await login.json()) as { access_token: string }
  const r = await fetch(`${process.env.E2E_API}/api/v1/users`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${access_token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD, role: 'user' }),
  })
  expect(r.status, await r.text()).toBe(201)
})

test('turns on two-factor with an authenticator', async ({ page }) => {
  await signIn(page, 'password')
  await signedIn(page)
  await page.goto('/account')
  await page.getByRole('button', { name: 'Set it up' }).click()
  const confirm = page.getByRole('dialog')
  await confirm.getByLabel('Your password').fill(PASSWORD)
  await confirm.getByRole('button', { name: 'Continue' }).click()

  const enroll = page.getByRole('dialog', { name: 'Set up two-factor sign-in' })
  secret = (await enroll.locator('code').textContent())!.trim()
  expect(secret).toMatch(/^[A-Z2-7]{16,}=*$/)
  // A wrong code is turned away and the dialog stays.
  await enroll.getByRole('textbox', { name: 'Code' }).fill(totp(secret) === '000000' ? '111111' : '000000')
  await enroll.getByRole('button', { name: 'Turn on' }).click()
  await expect(enroll).toBeVisible()
  await enroll.getByRole('textbox', { name: 'Code' }).fill(await freshCode(secret))
  await enroll.getByRole('button', { name: 'Turn on' }).click()

  recovery = await shownCodes(page)
  expect(recovery.length).toBeGreaterThanOrEqual(8)
  await page.getByRole('button', { name: 'I’ve saved them' }).click()
  await expect(page.getByText('On. Signing in asks for a code')).toBeVisible()
})

test('signing in now asks for a code, and a wrong one fails', async ({ page }) => {
  await signIn(page, 'code', totp(secret, Date.now() - 10 * 60_000))
  await expect(page.getByText('That code didn’t work.')).toBeVisible()
  await page.getByRole('textbox', { name: 'Two-factor code' }).fill(await freshCode(secret))
  await page.getByRole('button', { name: 'Continue' }).click()
  await signedIn(page)
})

test('a recovery code signs in once', async ({ page, browser }) => {
  await signIn(page, 'recovery', recovery[0])
  await signedIn(page)

  const again = await browser.newPage()
  try {
    await signIn(again, 'recovery', recovery[0])
    await expect(again.getByText('That code didn’t work.')).toBeVisible()
  } finally {
    await again.close()
  }
})

test('new recovery codes replace the old ones', async ({ page, browser }) => {
  await signIn(page, 'code', await freshCode(secret))
  await signedIn(page)
  await page.goto('/account')
  await page.getByRole('button', { name: 'New recovery codes' }).click()
  const confirm = page.getByRole('dialog')
  await confirm.getByLabel('Your password').fill(PASSWORD)
  await confirm.getByLabel('Two-factor code').fill(await freshCode(secret))
  await confirm.getByRole('button', { name: 'Make new codes' }).click()
  const fresh = await shownCodes(page)
  expect(fresh.length).toBeGreaterThanOrEqual(8)
  expect(fresh).not.toContain(recovery[1])
  await page.getByRole('button', { name: 'I’ve saved them' }).click()

  const old = await browser.newPage()
  try {
    await signIn(old, 'recovery', recovery[1])
    await expect(old.getByText('That code didn’t work.')).toBeVisible()
  } finally {
    await old.close()
  }
  recovery = fresh
})

test('turning it off goes back to password-only sign-in', async ({ page }) => {
  await signIn(page, 'recovery', recovery[0])
  await expect(page.getByText('That code didn’t work.')).toHaveCount(0)
  await signedIn(page)
  await page.goto('/account')
  await page.getByRole('button', { name: 'Turn off' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Continue' }).click()
  const confirm = page.getByRole('dialog')
  await confirm.getByLabel('Your password').fill(PASSWORD)
  await confirm.getByLabel('Two-factor code').fill(await freshCode(secret))
  await confirm.getByRole('button', { name: 'Turn off' }).click()
  await expect(page.getByText('Two-factor is off')).toBeVisible()

  await page.context().clearCookies()
  await page.evaluate(() => localStorage.clear())
  await signIn(page, 'password')
  await signedIn(page)
})
