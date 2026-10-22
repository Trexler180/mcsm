// An AI agent connecting over MCP the way Claude Code does: discovery from a
// 401, dynamic registration, the browser consent screen, a PKCE code
// exchange, then tools. Covers what the Go smoke test cannot: the consent page
// itself, the dev proxy's origin handling, and approving an agent's request in
// the dashboard.
import { expect, test, type Page } from '@playwright/test'
import { createHash, randomBytes } from 'node:crypto'

const WEB = 'http://127.0.0.1:13000'
const MCP = `${WEB}/api/v1/mcp`
const CALLBACK = 'http://127.0.0.1:18999/callback'
const SERVER = 'E2E MCP'

const b64url = (b: Buffer) => b.toString('base64url')

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

/** A minimal streamable-HTTP MCP client. */
class Session {
  private id = ''
  private seq = 0
  constructor(private token: string) {}

  async rpc(method: string, params: unknown = {}): Promise<{ status: number; result?: Record<string, unknown>; error?: { message: string } }> {
    const notify = method.startsWith('notifications/')
    const r = await fetch(MCP, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${this.token}`,
        'Content-Type': 'application/json',
        Accept: 'application/json, text/event-stream',
        ...(this.id ? { 'Mcp-Session-Id': this.id } : {}),
      },
      body: JSON.stringify({ jsonrpc: '2.0', method, params, ...(notify ? {} : { id: ++this.seq }) }),
    })
    this.id = r.headers.get('mcp-session-id') ?? this.id
    const text = await r.text()
    if (!r.ok) return { status: r.status, error: { message: `${r.status} ${text}` } }
    if (notify) return { status: r.status }
    // One JSON body, or a short SSE stream whose data line is the response.
    const json = r.headers.get('content-type')?.includes('text/event-stream')
      ? text
          .split('\n')
          .filter((l) => l.startsWith('data:'))
          .map((l) => l.slice(5).trim())
          .pop()!
      : text
    return { status: r.status, ...(JSON.parse(json) as object) }
  }

  async call(name: string, args: Record<string, unknown>) {
    const res = await this.rpc('tools/call', { name, arguments: args })
    expect(res.error, `${name}: ${res.error?.message}`).toBeUndefined()
    return res.result as { structuredContent?: Record<string, unknown>; isError?: boolean; content: { text: string }[] }
  }
}

test.describe.configure({ mode: 'serial' })

let page: Page
let serverId = ''
let token = ''

test.beforeAll(async ({ browser }) => {
  const node = (await api<{ id: string }[]>('GET', '/nodes'))[0]
  const versions = await api<{ version: string; stable: boolean }[]>('GET', '/minecraft/versions?platform=paper')
  const created = await api<{ id: string }>('POST', '/servers', {
    name: SERVER,
    node_id: node.id,
    platform: 'paper',
    mc_version: versions.find((v) => v.stable)!.version,
    port: 25598,
    ram_mb_max: 2048,
  })
  serverId = created.id

  page = await browser.newPage()
  await page.goto('/login')
  await page.locator('input[type=email]').fill(process.env.E2E_ADMIN_EMAIL!)
  await page.locator('input[type=password]').fill(process.env.E2E_ADMIN_PASSWORD!)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('navigation').first()).toBeVisible()
})

test.afterAll(async () => {
  await page.close()
  // It may still be on its first start, which only a forced kill interrupts.
  await fetch(`${process.env.E2E_API}/api/v1/servers/${serverId}/kill?force=true`, { method: 'POST', headers: { Authorization: `Bearer ${await adminToken()}` } })
  await expect
    .poll(async () => (await api<{ status: string }>('GET', `/servers/${serverId}`)).status, { timeout: 90_000 })
    .toMatch(/offline|stopped|crashed/)
  await api('DELETE', `/servers/${serverId}?files=true&backups=true`)
})

/**
 * Connects an agent the way Claude Code does, approving it in the browser, and
 * returns its token and a way to replay the code it was issued for.
 */
async function connect(clientName: string) {
  // Discovery starts from the 401 the endpoint gives without a token.
  const unauth = await fetch(MCP, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })
  expect(unauth.status).toBe(401)
  const challenge = unauth.headers.get('www-authenticate') ?? ''
  const metaURL = /resource_metadata="([^"]+)"/.exec(challenge)?.[1]
  expect(metaURL, challenge).toBeTruthy()
  // Named on the origin the client used, not the API's own port.
  expect(metaURL!.startsWith(WEB)).toBe(true)
  const resourceMeta = (await (await fetch(metaURL!)).json()) as { resource: string; authorization_servers: string[] }
  expect(resourceMeta.resource).toBe(MCP)

  const issuer = resourceMeta.authorization_servers[0]
  const asMeta = (await (await fetch(`${WEB}/.well-known/oauth-authorization-server${new URL(issuer).pathname.replace(/\/$/, '')}`)).json()) as {
    authorization_endpoint: string
    token_endpoint: string
    registration_endpoint: string
  }

  const reg = await fetch(asMeta.registration_endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      client_name: clientName,
      redirect_uris: [CALLBACK],
      token_endpoint_auth_method: 'none',
      grant_types: ['authorization_code', 'refresh_token'],
      response_types: ['code'],
    }),
  })
  expect(reg.status).toBe(201)
  const { client_id } = (await reg.json()) as { client_id: string }

  const verifier = b64url(randomBytes(32))
  const state = b64url(randomBytes(12))
  const auth = new URL(asMeta.authorization_endpoint)
  auth.search = new URLSearchParams({
    response_type: 'code',
    client_id,
    redirect_uri: CALLBACK,
    code_challenge: b64url(createHash('sha256').update(verifier).digest()),
    code_challenge_method: 'S256',
    state,
    resource: MCP,
    // What Claude Code asks for when it may request a restart.
    scope: 'mcp:servers.read mcp:diagnostics.read mcp:logs.read mcp:actions.request',
  }).toString()

  // Nothing listens on the callback; the browser's arrival there is the result.
  let landed = ''
  await page.route(`${CALLBACK}**`, (route) => {
    landed = route.request().url()
    return route.fulfill({ status: 200, contentType: 'text/plain', body: 'done' })
  })
  await page.goto(auth.toString())
  await expect(page.getByText(clientName).first()).toBeVisible()
  // Read access starts ticked; asking for actions and the server are opted into.
  await page.getByRole('checkbox', { name: 'Request lifecycle actions and version upgrades' }).check()
  await page.getByRole('checkbox', { name: SERVER }).check()
  await page.getByRole('button', { name: 'Allow access' }).click()
  await expect.poll(() => landed).not.toBe('')
  const back = new URL(landed)
  expect(back.searchParams.get('state')).toBe(state)
  const code = back.searchParams.get('code')!

  const exchange = (v: string) =>
    fetch(asMeta.token_endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({ grant_type: 'authorization_code', code, redirect_uri: CALLBACK, client_id, code_verifier: v, resource: MCP }),
    })
  const tok = await exchange(verifier)
  expect(tok.status).toBe(200)
  await page.unroute(`${CALLBACK}**`)
  return { token: ((await tok.json()) as { access_token: string }).access_token, replay: () => exchange(verifier) }
}

const init = (s: Session) => s.rpc('initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'e2e', version: '1' } })

test('an agent discovers, registers, and is approved in the browser', async () => {
  token = (await connect('E2E agent')).token
})

test('the agent reads the panel through its tools', async () => {
  const s = new Session(token)
  const init = await s.rpc('initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'e2e', version: '1' } })
  expect(init.result?.serverInfo, JSON.stringify(init)).toBeTruthy()
  await s.rpc('notifications/initialized')

  const tools = (await s.rpc('tools/list')).result as { tools: { name: string }[] }
  const names = tools.tools.map((t) => t.name)
  expect(names).toContain('list_servers')
  expect(names).toContain('request_server_action')

  const listed = await s.call('list_servers', {})
  expect(JSON.stringify(listed)).toContain(SERVER)
})

test('a start the agent asks for runs once a person approves it', async () => {
  const s = new Session(token)
  await s.rpc('initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'e2e', version: '1' } })
  await s.rpc('notifications/initialized')
  const asked = await s.call('request_server_action', { server_id: serverId, action: 'start', reason: 'The e2e suite checks the approval flow.' })
  const requestId = (asked.structuredContent as { request_id: string; status: string }).request_id
  expect((asked.structuredContent as { status: string }).status).toBe('pending')

  // The dashboard asks the person; nothing has started yet.
  await page.goto('/')
  expect((await api<{ status: string }>('GET', `/servers/${serverId}`)).status).toBe('offline')
  await expect(page.getByText(`E2E agent wants to start ${SERVER}.`)).toBeVisible()
  await page.getByRole('button', { name: 'Review' }).first().click()
  // Approving an agent's request takes the person's password.
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('The e2e suite checks the approval flow.')
  await dialog.locator('input[type=password]').fill(process.env.E2E_ADMIN_PASSWORD!)
  await dialog.getByRole('button', { name: 'Approve and run' }).click()
  await expect(page.getByText('Approved: start', { exact: false }).first()).toBeVisible()

  await expect
    .poll(async () => ((await s.call('get_action_request', { request_id: requestId })).structuredContent as { status: string }).status, { timeout: 60_000 })
    .toMatch(/executing|executed/)
  await expect
    .poll(async () => (await api<{ status: string }>('GET', `/servers/${serverId}`)).status, { timeout: 5 * 60_000 })
    .toMatch(/starting|running/)
})

test('a replayed code is refused and takes back the token it issued', async () => {
  const { token: second, replay } = await connect('E2E replay')
  const s = new Session(second)
  expect((await init(s)).status).toBe(200)
  expect((await replay()).status).toBe(400)
  expect((await init(new Session(second))).status).toBe(401)
})

test('disconnecting the agent in Account stops its token at once', async () => {
  await page.goto('/account/agents')
  // The replayed connection is already shown as disconnected, with no button.
  // (A reused stack can hold earlier runs' connections too.)
  const buttons = page.locator('.item', { hasText: 'E2E agent' }).getByRole('button', { name: 'Disconnect' })
  await expect(buttons.first()).toBeVisible()
  while ((await buttons.count()) > 0) {
    const before = await buttons.count()
    await buttons.first().click()
    await page.getByRole('dialog').getByRole('button', { name: 'Disconnect' }).click()
    await expect(page.getByText('Disconnected E2E agent').first()).toBeVisible()
    await expect(buttons).toHaveCount(before - 1)
  }

  const s = new Session(token)
  const after = await s.rpc('initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'e2e', version: '1' } })
  expect(after.status).toBe(401)
})
