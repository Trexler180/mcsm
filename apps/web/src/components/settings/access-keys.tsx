import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Bot, Copy, KeyRound, RefreshCw, ShieldAlert, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import { PERMISSION_MODEL, permissionLabel, collapsePerms, can } from '@/lib/permissions'
import { listPermissions } from '@/lib/server-permissions'
import { useNotifications } from '@/store/notifications'
import type { AccessKey, Server, ServerPermission } from '@/lib/types'

// The API refuses `admin` on a key outright — it would carry membership
// administration and server deletion — so it is not offered here either.
const SELECTABLE_GROUPS = PERMISSION_MODEL.filter((g) => g.group !== 'admin')

// The default a new key starts from: read-only diagnosis. `files.read` is
// deliberately not included — server files hold configuration secrets, so
// granting them has to be a deliberate tick rather than a silent default.
const DIAGNOSIS_PRESET: ServerPermission[] = ['view']

// Matches the API's MaxAccessKeyLifetime. Shorter is always allowed.
const MAX_LIFETIME_DAYS = 90
const DEFAULT_LIFETIME_DAYS = 30

// The date picker offers whole days, and a picked day is sent as its *end*
// (23:59:59 local) so the key survives the day it names. That end instant sits
// up to one day past "now + N days", while the API's cap is an exact
// 90 × 24h from the moment the request lands — so offering day 90 would let the
// form build a timestamp the API refuses. One day of headroom makes every
// selectable date land inside the cap, whatever time it is when the form is
// submitted. The stated maximum is still 90 days; this is only the picker's
// bound.
const MAX_SELECTABLE_DAYS = MAX_LIFETIME_DAYS - 1

function copy(text: string) {
  void navigator.clipboard?.writeText(text)
}

function fmtDate(t: string | null): string {
  if (!t) return '—'
  const d = new Date(t)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString()
}

/** yyyy-mm-dd in local calendar time for <input type="date">. */
function localDate(d: Date): string {
  const year = d.getFullYear()
  const month = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return year + '-' + month + '-' + day
}

/** yyyy-mm-dd, n local calendar days from today. */
function dateInDays(days: number): string {
  const d = new Date()
  d.setDate(d.getDate() + days)
  return localDate(d)
}

/** Last whole local day whose 23:59:59 instant remains inside the API's exact
 *  lifetime cap, including across DST changes and late-evening submissions. */
function latestSelectableDate(): string {
  const cap = new Date(Date.now() + MAX_LIFETIME_DAYS * 86_400_000)
  cap.setHours(0, 0, 0, 0)
  cap.setDate(cap.getDate() - 1)
  return localDate(cap)
}

/** A calendar date becomes an end-of-day RFC 3339 instant, so "expires on the
 *  30th" means the key survives that whole day rather than dying at midnight. */
function endOfDayISO(date: string): string {
  const d = new Date(`${date}T23:59:59`)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}

/** Whether the key's bounded lifetime has already run out. */
function isExpired(key: AccessKey): boolean {
  if (!key.expires_at) return false
  return new Date(key.expires_at).getTime() <= Date.now()
}

function keyState(key: AccessKey): { label: string; className: string } {
  if (key.revoked_at) {
    return { label: 'Revoked', className: 'border-border bg-surface-2 text-text-secondary' }
  }
  if (isExpired(key)) {
    return { label: 'Expired', className: 'border-border bg-surface-2 text-text-secondary' }
  }
  return { label: 'Active', className: 'border-green-500/30 bg-green-500/15 text-green-400' }
}

/** The one-time secret panel. Dismissing it drops the token from state; there is
 *  no way to get it back, which is the point. */
function SecretPanel({ token, onDismiss }: { token: string; onDismiss: () => void }) {
  return (
    <div className="rounded-md border border-amber-500/30 bg-amber-500/10 p-4 space-y-2">
      <p className="text-sm font-medium text-amber-300">Copy your key now</p>
      <p className="text-xs text-text-secondary">
        This is the only time it will be shown. Store it in your agent's secret
        manager or environment — never in a repository, a URL, or a chat log.
      </p>
      <div className="flex items-center gap-2">
        <code
          data-testid="access-key-secret"
          className="flex-1 break-all rounded bg-surface-2 px-3 py-2 font-mono text-sm text-text-primary"
        >
          {token}
        </code>
        <Button variant="outline" onClick={() => copy(token)} title="Copy key">
          <Copy className="h-4 w-4" />
        </Button>
      </div>
      <Button variant="outline" onClick={onDismiss}>
        I've saved it
      </Button>
    </div>
  )
}

/** Password (+ TOTP when enabled) fields shared by create and rotate. */
function StepUpFields({
  idPrefix,
  password,
  setPassword,
  totp,
  setTotp,
  mfaEnabled,
}: {
  idPrefix: string
  password: string
  setPassword: (v: string) => void
  totp: string
  setTotp: (v: string) => void
  mfaEnabled: boolean
}) {
  return (
    <div className="grid gap-2 sm:grid-cols-2">
      <div className="space-y-1.5">
        <Label htmlFor={`${idPrefix}-password`}>Confirm your password</Label>
        <Input
          id={`${idPrefix}-password`}
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </div>
      {mfaEnabled && (
        <div className="space-y-1.5">
          <Label htmlFor={`${idPrefix}-totp`}>Authentication code</Label>
          <Input
            id={`${idPrefix}-totp`}
            inputMode="numeric"
            autoComplete="one-time-code"
            placeholder="123456"
            value={totp}
            onChange={(e) => setTotp(e.target.value)}
          />
        </div>
      )}
    </div>
  )
}

function CreateKeyForm({
  servers,
  mfaEnabled,
  onCreated,
  onCancel,
}: {
  servers: Server[]
  mfaEnabled: boolean
  onCreated: (token: string) => void
  onCancel: () => void
}) {
  const qc = useQueryClient()
  const { error } = useNotifications()
  const [name, setName] = useState('')
  const [serverIDs, setServerIDs] = useState<string[]>([])
  const [scopes, setScopes] = useState<ServerPermission[]>(DIAGNOSIS_PRESET)
  const [expires, setExpires] = useState(dateInDays(DEFAULT_LIFETIME_DAYS))
  const [password, setPassword] = useState('')
  const [totp, setTotp] = useState('')

  const create = useMutation({
    mutationFn: () =>
      api.auth.apiKeys.create({
        name: name.trim(),
        server_ids: serverIDs,
        scopes: collapsePerms(scopes),
        expires_at: endOfDayISO(expires),
        password,
        totp_code: totp.trim() || undefined,
      }),
    onSuccess: (res) => {
      // Drop the credential material we were holding as soon as it's used.
      setPassword('')
      setTotp('')
      qc.invalidateQueries({ queryKey: ['access-keys'] })
      onCreated(res.token)
    },
    onError: (e: Error) => error('Could not create key', e.message),
  })

  const toggleServer = (id: string) =>
    setServerIDs((prev) => (prev.includes(id) ? prev.filter((s) => s !== id) : [...prev, id]))

  const toggleScope = (p: ServerPermission) =>
    setScopes((prev) => (prev.includes(p) ? prev.filter((s) => s !== p) : [...prev, p]))

  // The API re-checks all of this; the point of doing it here is to say why the
  // button is inert rather than to be the control.
  const ready =
    name.trim().length > 0 &&
    serverIDs.length > 0 &&
    scopes.length > 0 &&
    expires !== '' &&
    password.length > 0 &&
    (!mfaEnabled || totp.trim().length >= 6)

  return (
    <div className="space-y-4 rounded-md border border-border bg-surface-2/40 p-4">
      <div className="space-y-1.5">
        <Label htmlFor="key-name">Name</Label>
        <Input
          id="key-name"
          placeholder="Diagnosis agent"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <p className="text-xs text-text-secondary">
          Use one key per agent and purpose, so a single compromise is narrow and
          revoking it stops one thing.
        </p>
      </div>

      <div className="space-y-1.5">
        <Label>Servers</Label>
        {servers.length === 0 ? (
          <p className="text-xs text-text-secondary">
            You don't have access to any servers yet.
          </p>
        ) : (
          <div className="grid gap-1 sm:grid-cols-2">
            {servers.map((s) => (
              <label key={s.id} className="flex items-center gap-2 text-sm text-text-primary">
                <input
                  type="checkbox"
                  className="accent-accent"
                  aria-label={s.name}
                  checked={serverIDs.includes(s.id)}
                  onChange={() => toggleServer(s.id)}
                />
                <span className="truncate">{s.name}</span>
              </label>
            ))}
          </div>
        )}
        <p className="text-xs text-text-secondary">
          The key can only ever touch the servers you tick here.
        </p>
      </div>

      <div className="space-y-1.5">
        <Label>Permissions</Label>
        <div className="space-y-2">
          {SELECTABLE_GROUPS.map((g) => (
            <div key={g.group}>
              <label className="flex items-center gap-2 text-sm text-text-primary">
                <input
                  type="checkbox"
                  className="accent-accent"
                  // Named explicitly: several groups and leaves share words
                  // ("View" the server vs "View saved data"), and the label's
                  // surrounding prose would otherwise blur them together for
                  // screen readers too.
                  aria-label={permissionLabel(g.group)}
                  checked={scopes.includes(g.group)}
                  onChange={() => toggleScope(g.group)}
                />
                <span className="font-medium">{g.label}</span>
                <span className="text-xs text-text-secondary">{g.detail}</span>
              </label>
              {g.leaves.length > 0 && (
                <div className="ml-6 mt-1 flex flex-wrap gap-x-4 gap-y-1">
                  {g.leaves.map((leaf) => (
                    <label
                      key={leaf.value}
                      className="flex items-center gap-1.5 text-xs text-text-secondary"
                    >
                      <input
                        type="checkbox"
                        className="accent-accent"
                        aria-label={permissionLabel(leaf.value)}
                        checked={scopes.includes(g.group) || scopes.includes(leaf.value)}
                        disabled={scopes.includes(g.group)}
                        onChange={() => toggleScope(leaf.value)}
                      />
                      {leaf.label}
                    </label>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
        <p className="text-xs text-text-secondary">
          Console, file writes and power actions let an agent change or break a
          running server. Grant them only when the agent's job needs them, and
          keep diagnosis and action on separate keys where you can.
        </p>
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="key-expires">Expires</Label>
        <Input
          id="key-expires"
          type="date"
          value={expires}
          min={dateInDays(1)}
          max={latestSelectableDate()}
          onChange={(e) => setExpires(e.target.value)}
        />
        <p className="text-xs text-text-secondary">
          Required, and at most {MAX_LIFETIME_DAYS} days out. A chosen day is
          kept until its end, so the last date you can pick is{' '}
          {MAX_SELECTABLE_DAYS} days away. Shorter is better — rotate before it
          lapses.
        </p>
      </div>

      <StepUpFields
        idPrefix="key-create"
        password={password}
        setPassword={setPassword}
        totp={totp}
        setTotp={setTotp}
        mfaEnabled={mfaEnabled}
      />

      <div className="flex gap-2">
        <Button onClick={() => create.mutate()} loading={create.isPending} disabled={!ready}>
          Create key
        </Button>
        <Button variant="outline" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

function KeyRow({
  accessKey,
  serverNames,
  mfaEnabled,
  onRotated,
}: {
  accessKey: AccessKey
  serverNames: Map<string, string>
  mfaEnabled: boolean
  onRotated: (token: string) => void
}) {
  const qc = useQueryClient()
  const { success, error } = useNotifications()
  const [confirming, setConfirming] = useState<'rotate' | 'revoke' | null>(null)
  const [password, setPassword] = useState('')
  const [totp, setTotp] = useState('')

  const reset = () => {
    setConfirming(null)
    setPassword('')
    setTotp('')
  }

  const rotate = useMutation({
    mutationFn: () =>
      api.auth.apiKeys.rotate(accessKey.id, {
        password,
        totp_code: totp.trim() || undefined,
      }),
    onSuccess: (res) => {
      reset()
      qc.invalidateQueries({ queryKey: ['access-keys'] })
      onRotated(res.token)
    },
    onError: (e: Error) => error('Could not rotate key', e.message),
  })

  const revoke = useMutation({
    mutationFn: () => api.auth.apiKeys.revoke(accessKey.id),
    onSuccess: () => {
      reset()
      qc.invalidateQueries({ queryKey: ['access-keys'] })
      success('Key revoked', 'It stops working immediately.')
    },
    onError: (e: Error) => error('Could not revoke key', e.message),
  })

  const state = keyState(accessKey)
  const live = !accessKey.revoked_at
  // Rotation keeps every field but the secret, expiry included, so rotating an
  // expired key would mint a token that is already dead — the API answers 409.
  // Revoking one is still worth offering: it retires the row for good.
  const rotatable = live && !isExpired(accessKey)

  return (
    <div className="space-y-2 py-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-text-primary">{accessKey.name}</span>
            <span className={`rounded border px-1.5 py-0.5 text-[10px] ${state.className}`}>
              {state.label}
            </span>
            <code className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-text-secondary">
              {accessKey.token_prefix}…
            </code>
          </div>
          <div className="text-xs text-text-secondary">
            {accessKey.server_ids.map((id) => serverNames.get(id) ?? id).join(', ') ||
              'no servers'}
          </div>
          <div className="flex flex-wrap gap-1">
            {accessKey.scopes.map((s) => (
              <span
                key={s}
                className="rounded border border-border bg-surface-2 px-1.5 py-0.5 text-[10px] text-text-secondary"
              >
                {permissionLabel(s)}
              </span>
            ))}
          </div>
          <div className="text-xs text-text-secondary">
            Expires {fmtDate(accessKey.expires_at)} · last used{' '}
            {fmtDate(accessKey.last_used_at)}
            {accessKey.last_used_ip ? ` from ${accessKey.last_used_ip}` : ''}
            {accessKey.revoked_at ? ` · revoked ${fmtDate(accessKey.revoked_at)}` : ''}
          </div>
        </div>

        {live && confirming === null && (
          <div className="flex shrink-0 gap-2">
            {rotatable && (
              <Button variant="outline" size="sm" onClick={() => setConfirming('rotate')}>
                <RefreshCw className="h-3.5 w-3.5" />
                Rotate
              </Button>
            )}
            <Button variant="outline" size="sm" onClick={() => setConfirming('revoke')}>
              <Trash2 className="h-3.5 w-3.5 text-red-400" />
              Revoke
            </Button>
          </div>
        )}
      </div>

      {confirming === 'rotate' && (
        <div className="space-y-2 rounded-md border border-border bg-surface-2/40 p-3">
          <p className="text-xs text-text-secondary">
            Rotating issues a new secret and stops the current one immediately.
            Update the agent as soon as you have the new key.
          </p>
          <StepUpFields
            idPrefix={`rotate-${accessKey.id}`}
            password={password}
            setPassword={setPassword}
            totp={totp}
            setTotp={setTotp}
            mfaEnabled={mfaEnabled}
          />
          <div className="flex gap-2">
            <Button
              size="sm"
              onClick={() => rotate.mutate()}
              loading={rotate.isPending}
              disabled={!password || (mfaEnabled && totp.trim().length < 6)}
            >
              Rotate key
            </Button>
            <Button variant="outline" size="sm" onClick={reset}>
              Cancel
            </Button>
          </div>
        </div>
      )}

      {confirming === 'revoke' && (
        <div className="space-y-2 rounded-md border border-red-500/30 bg-red-500/10 p-3">
          <p className="text-xs text-text-secondary">
            Revoking is permanent and takes effect on the agent's next request.
            The key stays listed so its history keeps making sense.
          </p>
          <div className="flex gap-2">
            <Button
              variant="destructive"
              size="sm"
              onClick={() => revoke.mutate()}
              loading={revoke.isPending}
            >
              Revoke key
            </Button>
            <Button variant="outline" size="sm" onClick={reset}>
              Cancel
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

// AccessKeysCard is the owner-facing surface for machine credentials: what
// exists, what each one can reach, when it was last used, and how to rotate or
// revoke it.
export function AccessKeysCard() {
  const { success } = useNotifications()
  const [creating, setCreating] = useState(false)
  const [secret, setSecret] = useState<string | null>(null)

  const { data: keys = [], isLoading } = useQuery({
    queryKey: ['access-keys'],
    queryFn: api.auth.apiKeys.list,
  })
  const { data: servers = [] } = useQuery({
    queryKey: ['servers'],
    queryFn: () => api.servers.list(),
  })
  const { data: mfa } = useQuery({
    queryKey: ['mfa-status'],
    queryFn: api.auth.mfa.status,
  })

  const serverNames = new Map(servers.map((s) => [s.id, s.name]))
  // Only offer servers the caller can actually delegate on. The API enforces
  // this too; showing an unusable row would just produce a confusing rejection.
  const selectable = servers.filter((s) =>
    SELECTABLE_GROUPS.some((g) => can(listPermissions(s.permissions), g.group)),
  )

  return (
    <div className="rounded-lg border border-border bg-surface p-5 space-y-4">
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-accent/10">
            <Bot className="h-4 w-4 text-accent" />
          </div>
          <div>
            <h3 className="font-semibold text-text-primary">Agent access keys</h3>
            <p className="mt-1 text-sm text-text-secondary">
              Long-lived credentials for automation and AI agents. A key acts as
              you, with real authority over the servers you give it — bounded by
              the servers and permissions you pick, and by what you can still do
              yourself.
            </p>
          </div>
        </div>
        {!creating && (
          <Button
            variant="outline"
            className="shrink-0 whitespace-nowrap"
            onClick={() => setCreating(true)}
          >
            <KeyRound className="h-4 w-4" />
            New key
          </Button>
        )}
      </div>

      <div className="flex items-start gap-2 rounded-md border border-border bg-surface-2/40 p-3 text-xs text-text-secondary">
        <ShieldAlert className="mt-0.5 h-4 w-4 flex-shrink-0 text-amber-400" />
        <span>
          Send the key in an <code className="font-mono">Authorization: Bearer</code>{' '}
          header over HTTPS only. Never put it in a URL, and never hand an agent
          your password, a browser session, or a node token instead.
        </span>
      </div>

      {secret && <SecretPanel token={secret} onDismiss={() => setSecret(null)} />}

      {creating && (
        <CreateKeyForm
          servers={selectable}
          mfaEnabled={Boolean(mfa?.enabled)}
          onCreated={(token) => {
            setCreating(false)
            setSecret(token)
            success('Access key created')
          }}
          onCancel={() => setCreating(false)}
        />
      )}

      {isLoading ? (
        <div className="flex justify-center py-6">
          <div className="h-5 w-5 animate-spin rounded-full border-2 border-accent border-t-transparent" />
        </div>
      ) : keys.length === 0 ? (
        <p className="text-sm text-text-secondary">
          No access keys yet. Create one when an agent needs to reach a server on
          your behalf.
        </p>
      ) : (
        <div className="divide-y divide-border">
          {keys.map((k) => (
            <KeyRow
              key={k.id}
              accessKey={k}
              serverNames={serverNames}
              mfaEnabled={Boolean(mfa?.enabled)}
              onRotated={(token) => {
                setSecret(token)
                success('Access key rotated', 'The previous key no longer works.')
              }}
            />
          ))}
        </div>
      )}
    </div>
  )
}
