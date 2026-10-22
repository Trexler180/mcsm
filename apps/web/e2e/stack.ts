// Brings up a throwaway ServerManager for the end-to-end suite: a fresh
// database, its own agent and server folder, and the web app on its own ports,
// so the suite never touches a developer's panel or servers. Torn down (and its
// folder deleted) when the run ends.
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export const PORTS = { api: 18081, agent: 18090, web: 13000 }

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const exe = process.platform === 'win32' ? '.exe' : ''
// E2E_REUSE=1 leaves the stack running after a run and picks it up again on the
// next, so one test can be rerun (--grep) without rebuilding and restarting it.
const reuseFile = join(tmpdir(), 'mcsm-e2e-stack.json')

async function waitFor(url: string, what: string, ms = 120_000) {
  const until = Date.now() + ms
  for (;;) {
    try {
      const r = await fetch(url)
      if (r.ok) return
    } catch {
      // not up yet
    }
    if (Date.now() > until) throw new Error(`${what} did not come up at ${url}`)
    await new Promise((r) => setTimeout(r, 500))
  }
}

function start(cmd: string, args: string[], cwd: string, env: Record<string, string>, log: string): ChildProcess {
  const out = openSync(log, 'a')
  // A reused stack has to outlive the test runner, which takes its children with it.
  const keep = !!process.env.E2E_REUSE
  const p = spawn(cmd, args, { cwd, env: { ...process.env, ...env }, stdio: ['ignore', out, out], windowsHide: true, detached: keep })
  if (keep) p.unref()
  // The child has its own handle; holding ours keeps Windows from deleting the log.
  closeSync(out)
  return p
}

function stop(p: ChildProcess | undefined) {
  if (!p || p.exitCode !== null || p.pid === undefined) return
  if (process.platform === 'win32') {
    // Takes the Java servers the agent started with it.
    try {
      execFileSync('taskkill', ['/pid', String(p.pid), '/t', '/f'], { stdio: 'ignore' })
    } catch {
      // already gone
    }
  } else {
    p.kill('SIGTERM')
  }
}

async function up(url: string) {
  try {
    return (await fetch(url)).ok
  } catch {
    return false
  }
}

export default async function globalSetup() {
  if (process.env.E2E_REUSE && existsSync(reuseFile) && (await up(`http://127.0.0.1:${PORTS.api}/api/v1/health`))) {
    Object.assign(process.env, JSON.parse(readFileSync(reuseFile, 'utf-8')) as Record<string, string>)
    return
  }
  const dir = mkdtempSync(join(tmpdir(), 'mcsm-e2e-'))
  const servers = join(dir, 'servers')
  const data = join(dir, 'data')
  mkdirSync(servers)
  mkdirSync(data)
  const bin = join(dir, 'bin')
  const logs = join(dir, 'logs')
  mkdirSync(bin)
  mkdirSync(logs)

  // Built once rather than `go run`, so the processes the suite stops are the
  // servers themselves.
  execFileSync('go', ['build', '-o', join(bin, 'api' + exe), './cmd/server'], { cwd: join(repo, 'apps/api'), stdio: 'inherit' })
  execFileSync('go', ['build', '-o', join(bin, 'agent' + exe), './cmd/agent'], { cwd: join(repo, 'apps/agent'), stdio: 'inherit' })

  const agentToken = randomBytes(16).toString('hex')
  const adminPassword = randomBytes(12).toString('hex')
  const env = {
    E2E_DIR: dir,
    E2E_SERVERS: servers,
    E2E_ADMIN_EMAIL: 'e2e-admin@localhost.test',
    E2E_ADMIN_PASSWORD: adminPassword,
    E2E_API: `http://127.0.0.1:${PORTS.api}`,
  }
  Object.assign(process.env, env)

  const agent = start(join(bin, 'agent' + exe), [], dir, {
    MCSM_DEV_MODE: '1',
    AGENT_TOKEN: agentToken,
    AGENT_PORT: String(PORTS.agent),
    AGENT_SERVER_ROOT: servers,
  }, join(logs, 'agent.log'))
  const apiProc = start(join(bin, 'api' + exe), [], dir, {
    MCSM_DEV_MODE: '1',
    API_PORT: String(PORTS.api),
    DATABASE_PATH: join(data, 'mcsm.db'),
    JWT_SECRET: randomBytes(32).toString('hex'),
    SERVER_ROOT: servers,
    ADMIN_EMAIL: env.E2E_ADMIN_EMAIL,
    ADMIN_PASSWORD: adminPassword,
    AUTO_REGISTER_LOCAL_AGENT: '1',
    LOCAL_AGENT_PORT: String(PORTS.agent),
    LOCAL_AGENT_TOKEN: agentToken,
  }, join(logs, 'api.log'))
  const web = start(process.execPath, [join(repo, 'apps/web/node_modules/vite/bin/vite.js'), '--port', String(PORTS.web), '--strictPort'], join(repo, 'apps/web'), {
    PORT: String(PORTS.web),
    VITE_API_PORT: String(PORTS.api),
  }, join(logs, 'web.log'))

  const teardown = async () => {
    if (process.env.E2E_REUSE) {
      writeFileSync(reuseFile, JSON.stringify(env))
      return
    }
    stop(web)
    stop(apiProc)
    stop(agent)
    await new Promise((r) => setTimeout(r, 1000))
    if (!process.env.E2E_KEEP) rmSync(dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 500 })
  }
  try {
    await waitFor(`http://127.0.0.1:${PORTS.api}/api/v1/health`, 'the API')
    await waitFor(`http://127.0.0.1:${PORTS.web}/`, 'the web app')
  } catch (e) {
    console.error(`e2e stack failed to start; logs in ${logs}`)
    process.env.E2E_KEEP = '1'
    await teardown()
    throw e
  }
  return teardown
}
