import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { can, collapsePerms } from '@/lib/permissions'
import { listPermissions } from '@/lib/server-permissions'
import type { AccessKey, Server, ServerPermission } from '@/lib/types'
import { useAuthStore } from '@/store/auth'
import { Btn, Check, Empty, Field, IconBtn, Select } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtDate, relTime } from '@/ui/format'
import { Dialog } from '@/ui/overlays'
import { Loaded, SkItems } from '@/ui/skeleton'
import { ask, stepUp } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { copyText, cx, toMs } from '@/ui/util'
import { PermissionEditor, permSummary } from '../server/settings-access'

// Access keys: tokens for scripts, exporters and agents that act as you on
// the servers and with the permissions you pick, never more than you can do,
// and never for longer than 90 days. Making or replacing one asks for your
// password; the token is shown once.

const LIFETIMES = [
  { value: '7', label: '7 days' },
  { value: '30', label: '30 days' },
  { value: '60', label: '60 days' },
  // The API caps a key at exactly 90 days from the request; a day of headroom
  // keeps "end of the 89th day" inside it whatever the hour.
  { value: '89', label: '89 days, the longest' },
]

/** The end of the day `days` from now, as the key's expiry. */
export function expiryIn(days: number, now = new Date()): string {
  const d = new Date(now)
  d.setDate(d.getDate() + days)
  d.setHours(23, 59, 59, 0)
  return d.toISOString()
}

export function keyState(k: Pick<AccessKey, 'revoked_at' | 'expires_at'>, now = Date.now()): 'revoked' | 'expired' | 'live' {
  if (k.revoked_at) return 'revoked'
  const exp = toMs(k.expires_at)
  return exp != null && exp <= now ? 'expired' : 'live'
}

function TokenDialog({ token, onClose }: { token: string; onClose: () => void }) {
  return (
    <Dialog open onClose={onClose} title="Copy the key now" foot={<Btn variant="primary" onClick={onClose}>Done</Btn>}>
      <div className="stack" style={{ gap: 12 }}>
        <p className="t2">You won’t see it again, so put it somewhere safe. Send it as a Bearer token over HTTPS; never in a link.</p>
        <div className="codeblock">
          {token}
          <IconBtn
            icon="copy"
            label="Copy"
            onClick={() => {
              void copyText(token)
              toast('ok', 'Copied the key')
            }}
          />
        </div>
      </div>
    </Dialog>
  )
}

export function KeysSection() {
  const qc = useQueryClient()
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  const keys = useQuery({ queryKey: ['access-keys'], queryFn: () => api.auth.apiKeys.list() })
  const servers = useQuery({ queryKey: ['servers'], queryFn: ({ signal }) => api.servers.list(signal) }).data ?? []
  const [creating, setCreating] = useState(false)
  const [token, setToken] = useState<string | null>(null)
  const names = new Map(servers.map((s) => [s.id, s.name]))
  // Only servers you could delegate something on; the API checks too.
  const usable = admin ? servers : servers.filter((s) => s.permissions?.length && can(listPermissions(s.permissions), 'view'))
  const refresh = () => void qc.invalidateQueries({ queryKey: ['access-keys'] })

  const rotate = async (k: AccessKey) => {
    const v = await stepUp({ title: `Replace ${k.name}?`, body: 'It gets a new token and the current one stops working straight away. Update whatever uses it.', confirm: 'Replace' })
    if (!v) return
    const r = await act(`replace ${k.name}`, () => api.auth.apiKeys.rotate(k.id, { password: v.password, totp_code: v.code || undefined }))
    if (!r) return
    setToken(r.token)
    refresh()
  }
  const revoke = async (k: AccessKey) => {
    if (!(await ask({ title: `Revoke ${k.name}?`, body: 'Anything using it stops working straight away. It stays listed so its history makes sense.', confirm: 'Revoke', danger: true }))) return
    const r = await act(`revoke ${k.name}`, () => api.auth.apiKeys.revoke(k.id).then(() => true))
    if (!r) return
    toast('ok', `Revoked ${k.name}`)
    refresh()
  }

  return (
    <div>
      <div className="row" style={{ marginBottom: 14, alignItems: 'flex-start', gap: 16 }}>
        <p className="t2 grow" style={{ maxWidth: '60ch' }}>
          Tokens for scripts, exporters and AI agents. A key acts as you, but only on the servers and with the permissions you give it, and never for more than 90 days. Use one per job, so revoking one stops one thing.
        </p>
        <Btn onClick={() => setCreating(true)}>Make a key</Btn>
      </div>
      <Loaded query={keys} sk={<SkItems rows={2} />} what="your access keys">
        {(list) =>
          list.length === 0 ? (
            <Empty title="No access keys yet." />
          ) : (
            <div className="items">
              {list.map((k) => {
                const state = keyState(k)
                return (
                  <div key={k.id} className={cx('item', state !== 'live' && 'muted')} style={{ alignItems: 'flex-start' }}>
                    <div className="grow" style={{ minWidth: 0 }}>
                      <div className="title">
                        {k.name}{' '}
                        <span className="mono t3 small" style={{ fontWeight: 400 }}>
                          {k.token_prefix}…
                        </span>
                      </div>
                      <div className="meta">
                        {permSummary(k.scopes)} on {k.server_ids.map((id) => names.get(id) ?? 'a server you can’t see').join(', ') || 'no servers'}.
                      </div>
                      <div className="meta">
                        {state === 'revoked' ? (
                          `Revoked ${relTime(toMs(k.revoked_at))}`
                        ) : state === 'expired' ? (
                          <span className="warn">Expired {relTime(toMs(k.expires_at))}</span>
                        ) : (
                          `Expires ${fmtDate(toMs(k.expires_at) ?? 0, false)}`
                        )}
                        . {k.last_used_at ? <>Last used {relTime(toMs(k.last_used_at))}{k.last_used_ip && <> from <span className="mono">{k.last_used_ip}</span></>}.</> : 'Never used.'}
                      </div>
                    </div>
                    {state === 'live' && (
                      <Btn size="sm" onClick={() => void rotate(k)}>
                        Replace
                      </Btn>
                    )}
                    {state !== 'revoked' && (
                      <Btn size="sm" variant="danger" onClick={() => void revoke(k)}>
                        Revoke
                      </Btn>
                    )}
                  </div>
                )
              })}
            </div>
          )
        }
      </Loaded>
      {creating && (
        <CreateKeyDialog
          servers={usable}
          onClose={() => setCreating(false)}
          onCreated={(t) => {
            setCreating(false)
            setToken(t)
            refresh()
          }}
        />
      )}
      {token && <TokenDialog token={token} onClose={() => setToken(null)} />}
    </div>
  )
}

function CreateKeyDialog({ servers, onClose, onCreated }: { servers: Server[]; onClose: () => void; onCreated: (token: string) => void }) {
  const [name, setName] = useState('')
  const [picked, setPicked] = useState<string[]>(servers.length === 1 ? [servers[0].id] : [])
  // Starts at read-only: anything that changes a server is a deliberate tick.
  const [scopes, setScopes] = useState<ServerPermission[]>(['view'])
  const [days, setDays] = useState('30')
  const [touched, setTouched] = useState(false)
  const errors = {
    name: name.trim() ? null : 'Give it a name, like what will use it.',
    servers: picked.length ? null : 'Pick at least one server.',
    scopes: scopes.length ? null : 'Pick at least one permission.',
  }
  const create = async () => {
    setTouched(true)
    if (errors.name || errors.servers || errors.scopes) return
    const v = await stepUp({ title: 'Confirm it’s you', body: `Making “${name.trim()}”, which can act as you.`, confirm: 'Make the key' })
    if (!v) return
    const r = await act(`make ${name.trim()}`, () =>
      api.auth.apiKeys.create({ name: name.trim(), server_ids: picked, scopes: collapsePerms(scopes), expires_at: expiryIn(Number(days)), password: v.password, totp_code: v.code || undefined }),
    )
    if (r) onCreated(r.token)
  }
  const err = (k: keyof typeof errors) => (touched ? errors[k] : null)
  return (
    <Dialog
      open
      onClose={onClose}
      size="wide"
      title="Make an access key"
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" onClick={() => void create()}>
            Make key
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 18 }}>
        <div className="form-grid">
          <Field label="Name" error={err('name')}>
            <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="Grafana exporter" autoFocus aria-label="Name" />
          </Field>
          <Field label="Expires after">
            <Select value={days} onChange={setDays} options={LIFETIMES} aria-label="Expires after" />
          </Field>
        </div>
        <Field label="Servers" error={err('servers')} hint={servers.length ? undefined : 'You don’t have any servers to give it.'}>
          <div className="row wrap" style={{ gap: '8px 18px' }}>
            {servers.map((s) => (
              <Check key={s.id} checked={picked.includes(s.id)} onChange={(v) => setPicked(v ? [...picked, s.id] : picked.filter((x) => x !== s.id))} label={s.name} />
            ))}
          </div>
        </Field>
        <Field label="It can" error={err('scopes')} hint="Never more than you can do yourself. Console, files and power let it change or break a running server.">
          <PermissionEditor value={scopes} onChange={(v) => setScopes(v as ServerPermission[])} without={['admin']} />
        </Field>
      </div>
    </Dialog>
  )
}
