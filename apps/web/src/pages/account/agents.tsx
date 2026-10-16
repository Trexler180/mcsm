import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { MCPActionRequest, MCPApprovalPolicy, MCPConnectionInfo, MCPGrant, MCPGrantPolicyOverrides, MCPScope } from '@/lib/types'
import { Btn, Empty, IconBtn, Notice, Select, Switch } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtDate, relTime } from '@/ui/format'
import { Dialog } from '@/ui/overlays'
import { Loaded, SkItems } from '@/ui/skeleton'
import { ask, stepUp } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { copyText, cx, toMs } from '@/ui/util'

// AI agents that connect over MCP: how to connect one (it's approved in the
// browser, so there's no token to paste), what they've asked to do, the
// default rules for what needs asking, and each connection with its own
// rules. Loosening a rule asks for your password; tightening never does.

const SCOPE_LABELS: Record<MCPScope, string> = {
  'mcp:servers.read': 'see servers',
  'mcp:diagnostics.read': 'read diagnoses',
  'mcp:logs.read': 'read warnings and errors',
  'mcp:metrics.read': 'read performance history',
  'mcp:audit.read': 'read the audit log',
  'mcp:logs.raw': 'read the raw console and crash reports',
  'mcp:config.read': 'read config files',
  'mcp:players.read': 'see player names',
  'mcp:actions.request': 'ask to start, stop, restart or upgrade',
  'mcp:power.start': 'start servers',
  'mcp:power.stop': 'stop servers',
  'mcp:power.restart': 'restart servers',
  'mcp:mods.read': 'look at mods',
  'mcp:mods.install': 'install mods',
  'mcp:mods.update': 'update and turn off mods',
  'mcp:mods.remove': 'remove mods',
  'mcp:backups.create': 'ask for backups',
  'mcp:players.whitelist': 'change the whitelist',
  'mcp:console.run': 'run a short list of console commands',
}

/** Scopes an agent acts on the moment it decides to, with nobody asked. */
const ACTS_ALONE = new Set<MCPScope>(['mcp:power.start', 'mcp:power.stop', 'mcp:power.restart', 'mcp:mods.install', 'mcp:mods.update', 'mcp:mods.remove', 'mcp:players.whitelist', 'mcp:console.run'])

export function scopeSentence(scopes: MCPScope[]): { reads: string; acts: string } {
  const reads = scopes.filter((s) => !ACTS_ALONE.has(s)).map((s) => SCOPE_LABELS[s] ?? s)
  const acts = scopes.filter((s) => ACTS_ALONE.has(s)).map((s) => SCOPE_LABELS[s] ?? s)
  const list = (xs: string[]) => (xs.length > 1 ? xs.slice(0, -1).join(', ') + ' and ' + xs[xs.length - 1] : xs[0] ?? '')
  return { reads: list(reads), acts: list(acts) }
}

type RuleKey = keyof MCPApprovalPolicy

const RULES: { key: RuleKey; name: string; desc: string; strict: string; loose: string; loosen: string; looseValue: boolean }[] = [
  {
    key: 'require_password',
    name: 'Ask for my password before approving',
    desc: 'And a two-factor code, if you use one.',
    strict: 'Ask for my password',
    loose: 'One click',
    loosen: 'Requests will be approved with one click, without your password.',
    looseValue: false,
  },
  {
    key: 'auto_approve_lifecycle',
    name: 'Let agents start, stop and restart servers without asking',
    desc: 'You still get an alert afterwards.',
    strict: 'Ask me first',
    loose: 'Don’t ask',
    loosen: 'Agents will be able to start, stop and restart servers without asking you.',
    looseValue: true,
  },
  {
    key: 'auto_approve_upgrades',
    name: 'Let agents change Minecraft versions without asking',
    desc: 'A version change rewrites every mod and can roll the world back. Most people leave this off.',
    strict: 'Ask me first',
    loose: 'Don’t ask',
    loosen: 'Agents will be able to change Minecraft versions without asking you. That rewrites every mod and can roll a world back.',
    looseValue: true,
  },
]

/** The rules that going from `from` to `to` loosens. Mirrors the API, which decides. */
export function loosened(from: MCPApprovalPolicy, to: MCPApprovalPolicy) {
  return RULES.filter((r) => from[r.key] !== r.looseValue && to[r.key] === r.looseValue)
}

/** Runs a rules change, asking for the password first when it loosens anything. */
async function changeRules(from: MCPApprovalPolicy, to: MCPApprovalPolicy, save: (auth: { password?: string; totp_code?: string }) => Promise<unknown>): Promise<boolean> {
  const looser = loosened(from, to)
  let auth = {}
  if (looser.length) {
    const v = await stepUp({ title: 'Confirm it’s you', body: looser.map((r) => r.loosen).join(' ') + ' Confirm it’s you to make that change.', confirm: 'Confirm and save' })
    if (!v) return false
    auth = { password: v.password, totp_code: v.code || undefined }
  }
  const r = await act('change the approval rules', () => save(auth).then(() => true))
  if (!r) return false
  toast('ok', 'Saved the approval rules', looser.length ? undefined : 'Tighter rules apply straight away.')
  return true
}

export function AgentsSection() {
  const connection = useQuery({ queryKey: ['mcp-connection'], queryFn: () => api.mcp.connection(), staleTime: 5 * 60_000 })
  return (
    <div className="stack">
      <div className="form-section">
        <h2>Connecting an agent</h2>
        <Loaded query={connection} sk={<SkItems rows={1} />} what="how to connect">
          {(info) => <Connect info={info} />}
        </Loaded>
      </div>
      <Requests />
      <Defaults />
      <Connected />
    </div>
  )
}

function Snippet({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ marginTop: 12 }}>
      <div className="field-label" style={{ marginBottom: 6 }}>
        {label}
      </div>
      <div className="codeblock">
        {value}
        <IconBtn
          icon="copy"
          label={`Copy the ${label.toLowerCase()}`}
          onClick={() => {
            void copyText(value)
            toast('ok', 'Copied')
          }}
        />
      </div>
    </div>
  )
}

function Connect({ info }: { info: MCPConnectionInfo }) {
  const [more, setMore] = useState(false)
  if (!info.configured) {
    return <Notice tone="warn">Agents can’t connect yet: the panel doesn’t know its public address. Set it in the panel’s configuration, then reload.</Notice>
  }
  return (
    <>
      <p className="lede">Claude Code, Codex and other MCP clients connect with this. You approve each one in the browser and pick what it can do, so there’s no token to paste anywhere.</p>
      <Snippet label="Claude Code" value={info.claude_code} />
      {more ? (
        <>
          <Snippet label="Codex (config.toml)" value={info.codex_config} />
          <Snippet label="Hermes (~/.hermes/config.yaml)" value={info.hermes_config} />
          <Snippet label="Server address" value={info.url} />
        </>
      ) : (
        <p className="small" style={{ marginTop: 10 }}>
          <button type="button" className="link quiet" onClick={() => setMore(true)}>
            Other clients
          </button>
        </p>
      )}
    </>
  )
}

const actionText = (a: string) => (a.startsWith('upgrade:') ? `change to Minecraft ${a.slice(8)}` : a)

function Requests() {
  const qc = useQueryClient()
  const requests = useQuery({ queryKey: ['mcp-action-requests'], queryFn: () => api.mcp.actionRequests.list(), refetchInterval: 30_000 })
  const recent = (requests.data ?? []).filter((r) => r.status === 'pending' || Date.now() - (toMs(r.created_at) ?? 0) < 7 * 86_400_000).slice(0, 10)
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['mcp-action-requests'] })
    void qc.invalidateQueries({ queryKey: ['fleet'] })
  }
  const approve = async (r: MCPActionRequest) => {
    let auth: { password?: string; totp_code?: string } | undefined
    if (r.requires_password) {
      const v = await stepUp({ title: `Let ${r.client_name} ${actionText(r.action)} ${r.server_name}?`, body: 'This runs once, straight away.', confirm: 'Approve' })
      if (!v) return
      auth = { password: v.password, totp_code: v.code || undefined }
    }
    const res = await act(`approve ${r.client_name}’s request`, () => api.mcp.actionRequests.approve(r.id, auth))
    if (!res) return
    if (res.status === 'failed') toast('bad', 'It didn’t work', res.failure_reason ?? 'The server didn’t accept it.')
    else toast('ok', `Approved: ${actionText(r.action)} ${r.server_name}`)
    refresh()
  }
  const deny = (r: MCPActionRequest) =>
    void act(`refuse ${r.client_name}’s request`, () => api.mcp.actionRequests.deny(r.id)).then((res) => {
      if (!res) return
      toast('ok', 'Refused')
      refresh()
    })
  if (!recent.length) return null
  return (
    <div className="form-section">
      <h2>What agents asked to do</h2>
      <div className="items flush">
        {recent.map((r) => (
          <div key={r.id} className="item" style={{ alignItems: 'flex-start' }}>
            <div className="grow" style={{ minWidth: 0 }}>
              <div className="title">
                {r.client_name} asked to {actionText(r.action)} {r.server_name}
              </div>
              <div className="meta">
                {relTime(toMs(r.created_at))}.{' '}
                {r.status === 'pending' ? (
                  <>Waiting for you until {fmtDate(toMs(r.expires_at) ?? 0)}.</>
                ) : r.status === 'executed' ? (
                  'It ran.'
                ) : r.status === 'failed' ? (
                  <span className="bad">It failed: {r.failure_reason}</span>
                ) : (
                  r.status.charAt(0).toUpperCase() + r.status.slice(1) + '.'
                )}
              </div>
              {r.reason && (
                <div className="untrusted small" title="Written by the agent. Judge it against what you can see.">
                  <span className="t3">The agent says: </span>
                  {r.reason}
                </div>
              )}
            </div>
            {r.status === 'pending' && (
              <>
                <Btn size="sm" variant="primary" onClick={() => void approve(r)}>
                  Approve
                </Btn>
                <Btn size="sm" onClick={() => deny(r)}>
                  Refuse
                </Btn>
              </>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

function Defaults() {
  const qc = useQueryClient()
  const policy = useQuery({ queryKey: ['mcp-approval-settings'], queryFn: () => api.mcp.approvalSettings.get() })
  const set = (pol: MCPApprovalPolicy, key: RuleKey, value: boolean) => {
    const next = { ...pol, [key]: value }
    void changeRules(pol, next, (auth) => api.mcp.approvalSettings.set({ ...next, ...auth })).then((ok) => {
      if (!ok) return
      void qc.invalidateQueries({ queryKey: ['mcp-approval-settings'] })
      void qc.invalidateQueries({ queryKey: ['mcp-grants'] })
    })
  }
  return (
    <div className="form-section">
      <h2>When an agent asks to do something</h2>
      <p className="lede">Your defaults. Each connected agent can have its own rules, below.</p>
      <Loaded query={policy} sk={<SkItems rows={3} />} what="your approval rules">
        {(pol) => (
          <>
            {RULES.map((r) => (
              <div key={r.key} className="setting">
                <div className="text">
                  <div className="name">{r.name}</div>
                  <div className="desc">{r.desc}</div>
                </div>
                <Switch checked={pol[r.key]} label={r.name} onChange={(v) => set(pol, r.key, v)} />
              </div>
            ))}
          </>
        )}
      </Loaded>
    </div>
  )
}

function Connected() {
  const qc = useQueryClient()
  const grants = useQuery({ queryKey: ['mcp-grants'], queryFn: () => api.mcp.grants.list() })
  const [rules, setRules] = useState<MCPGrant | null>(null)
  const disconnect = async (g: MCPGrant) => {
    if (!(await ask({ title: `Disconnect ${g.client_name}?`, body: 'It stops working straight away. Connecting it again means approving it again.', confirm: 'Disconnect', danger: true }))) return
    const r = await act(`disconnect ${g.client_name}`, () => api.mcp.grants.revoke(g.id).then(() => true))
    if (!r) return
    toast('ok', `Disconnected ${g.client_name}`)
    void qc.invalidateQueries({ queryKey: ['mcp-grants'] })
  }
  return (
    <div className="form-section" style={{ borderBottom: 0 }}>
      <h2>Connected</h2>
      <Loaded query={grants} sk={<SkItems rows={2} />} what="your connected agents">
        {(list) =>
          list.length === 0 ? (
            <Empty title="No agents connected." />
          ) : (
            <div className="items flush">
              {list.map((g) => {
                const s = scopeSentence(g.scopes)
                const own = RULES.filter((r) => g.overrides[r.key] != null)
                return (
                  <div key={g.id} className={cx('item', !g.active && 'muted')} style={{ alignItems: 'flex-start' }}>
                    <div className="grow" style={{ minWidth: 0 }}>
                      <div className="title">
                        {g.client_name}{' '}
                        <span className="t3 small" style={{ fontWeight: 400 }}>
                          {g.active ? `connected ${relTime(toMs(g.created_at))}` : g.revoked_at ? 'disconnected' : `expired ${relTime(toMs(g.expires_at))}`}
                        </span>
                      </div>
                      <div className="meta">On {g.servers.map((x) => x.name || x.id).join(', ') || 'no servers'}.</div>
                      {s.reads && <div className="meta">Can {s.reads}.</div>}
                      {s.acts && <div className="meta warn">Can {s.acts} without asking.</div>}
                      {g.active && (
                        <div className="meta">
                          {g.last_used_at ? `Last used ${relTime(toMs(g.last_used_at))}` : 'Not used yet'}. Expires {relTime(toMs(g.expires_at))}.{' '}
                          {own.length ? `Own rules: ${own.map((r) => (g.overrides[r.key] === r.looseValue ? r.loose : r.strict).toLowerCase()).join(', ')}.` : 'Follows your defaults.'}
                        </div>
                      )}
                    </div>
                    {g.active && (
                      <>
                        <Btn size="sm" onClick={() => setRules(g)}>
                          Rules
                        </Btn>
                        <Btn size="sm" variant="danger" onClick={() => void disconnect(g)}>
                          Disconnect
                        </Btn>
                      </>
                    )}
                  </div>
                )
              })}
            </div>
          )
        }
      </Loaded>
      {rules && <GrantRules grant={rules} onClose={() => setRules(null)} />}
    </div>
  )
}

type Choice = 'default' | 'strict' | 'loose'

function GrantRules({ grant, onClose }: { grant: MCPGrant; onClose: () => void }) {
  const qc = useQueryClient()
  const [d, setD] = useState<MCPGrantPolicyOverrides>({ ...grant.overrides })
  // The account defaults under this connection: what "Use my default" means.
  const defaults = useQuery({ queryKey: ['mcp-approval-settings'], queryFn: () => api.mcp.approvalSettings.get() }).data
  const resolve = (o: MCPGrantPolicyOverrides, base: MCPApprovalPolicy): MCPApprovalPolicy => ({
    require_password: o.require_password ?? base.require_password,
    auto_approve_lifecycle: o.auto_approve_lifecycle ?? base.auto_approve_lifecycle,
    auto_approve_upgrades: o.auto_approve_upgrades ?? base.auto_approve_upgrades,
  })
  const toChoice = (r: (typeof RULES)[number], v: boolean | null): Choice => (v == null ? 'default' : v === r.looseValue ? 'loose' : 'strict')
  const fromChoice = (r: (typeof RULES)[number], c: Choice): boolean | null => (c === 'default' ? null : c === 'loose' ? r.looseValue : !r.looseValue)
  const save = async () => {
    const base = defaults ?? grant.policy
    const ok = await changeRules(grant.policy, resolve(d, base), (auth) => api.mcp.grants.setApproval(grant.id, { ...d, ...auth }))
    if (!ok) return
    void qc.invalidateQueries({ queryKey: ['mcp-grants'] })
    onClose()
  }
  return (
    <Dialog
      open
      onClose={onClose}
      size="wide"
      title={`Rules for ${grant.client_name}`}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" onClick={() => void save()}>
            Save
          </Btn>
        </>
      }
    >
      <p className="t2" style={{ marginBottom: 6 }}>
        These win over your defaults in either direction: an agent you trust less can be held to more than the default.
      </p>
      {RULES.map((r) => (
        <div key={r.key} className="setting">
          <div className="text">
            <div className="name">{r.name.replace(/^Let agents /, 'Let it ').replace(/^Ask for my password/, 'Ask for my password')}</div>
            {defaults && <div className="desc">Your default: {(defaults[r.key] === r.looseValue ? r.loose : r.strict).toLowerCase()}.</div>}
          </div>
          <Select
            style={{ width: 220 }}
            value={toChoice(r, d[r.key])}
            onChange={(v) => setD({ ...d, [r.key]: fromChoice(r, v) })}
            aria-label={r.name}
            options={[
              { value: 'default', label: 'Use my default' },
              { value: 'strict', label: r.strict },
              { value: 'loose', label: r.loose },
            ]}
          />
        </div>
      ))}
    </Dialog>
  )
}
