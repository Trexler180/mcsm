// Webhooks from the Delivery page. Delivery itself can't be watched from here:
// the panel won't post to this machine or the local network (a webhook URL is
// an address the panel fetches on someone's say-so), so the suite checks that
// refusal from the person's side instead.
import { expect, test } from '@playwright/test'

test('a webhook to this machine is added but never delivered to', async ({ page }) => {
  await page.goto('/login')
  await page.locator('input[type=email]').fill(process.env.E2E_ADMIN_EMAIL!)
  await page.locator('input[type=password]').fill(process.env.E2E_ADMIN_PASSWORD!)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('navigation').first()).toBeVisible()
  await page.goto('/alerts/delivery')

  const url = page.getByRole('textbox', { name: 'URL' })
  await url.fill('http://example.com/hook')
  await expect(page.getByText('Use an https:// address.')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Add webhook' })).toBeDisabled()

  await page.getByRole('button', { name: 'Format' }).click()
  await page.getByRole('option', { name: 'Plain JSON' }).click()
  await page.getByPlaceholder('Staff Discord').fill('E2E loopback')
  await url.fill('https://127.0.0.1:18998/hook')
  await page.getByRole('button', { name: 'Add webhook' }).click()
  await expect(page.getByText('Added E2E loopback')).toBeVisible()

  const row = page.locator('.item', { hasText: 'E2E loopback' })
  await row.getByRole('button', { name: 'Send a test' }).click()
  await expect(page.locator('.toast-text', { hasText: /E2E loopback|send a test/i }).first()).toBeVisible()
  await expect(page.getByText('Sent a test to E2E loopback')).toHaveCount(0)

  await row.getByRole('button', { name: 'Delete E2E loopback' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText('Deleted E2E loopback')).toBeVisible()
  await expect(row).toHaveCount(0)
})
