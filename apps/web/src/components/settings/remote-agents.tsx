import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Check,
  Copy,
  Plug,
  ShieldAlert,
  SlidersHorizontal,
  Trash2,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { ApprovalPolicyCard, GrantPolicyPanel } from './approval-policy'
import { StepUpFields, stepUpReady, useMfaEnabled } from './step-up-fields'
import { api } from '@/lib/api'
import { useNotifications } from '@/store/notifications'
import type {
  MCPActionRequest,
  MCPConnectionInfo,
  MCPGrant,
  MCPScope,
} from '@/lib/types'

// Short labels for the scope vocabulary. The consent screen gets its wording
// from the API (it must describe a request this build may not know about); a
// grant only ever holds scopes this build issued, so naming them here keeps the
// list readable without a round trip.
const SCOPE_LABELS: Record<MCPScope, string> = {
  'mcp:servers.read': 'See servers',
  'mcp:diagnostics.read': 'Diagnostics',
  'mcp:logs.read': 'Log events',
  'mcp:metrics.read': 'Performance history',
  'mcp:audit.read': 'Audit trail',
  'mcp:actions.request': 'Request lifecycle/version upgrades',
  'mcp:power.start': 'Start servers',
  'mcp:power.stop': 'Stop servers',
  'mcp:power.restart': 'Restart servers',
  'mcp:mods.read': 'Inspect mods',
  'mcp:mods.install': 'Install mods',
  'mcp:mods.update': 'Update/disable mods',
  'mcp:mods.remove': 'Remove mods',
  'mcp:backups.create': 'Request backups (human confirmation every time)',
  'mcp:players.whitelist': 'Add/remove whitelist entries',
  'mcp:console.run': 'Run allowlisted console commands',
}

// Capabilities that change a server the moment the agent calls them, with no
// further prompt. `mcp:actions.request` and `mcp:backups.create` are
// deliberately not among them: both require a separate human decision, while
// `mcp:mods.read` only reads.
const EXECUTING_SCOPES = new Set<MCPScope>([
  'mcp:power.start',
  'mcp:power.stop',
  'mcp:power.restart',
  'mcp:mods.install',
  'mcp:mods.update',
  'mcp:mods.remove',
  'mcp:players.whitelist',
  'mcp:console.run',
])

const ACTION_LABELS: Record<string, string> = {
  start: 'Start',
  stop: 'Stop',
  restart: 'Restart',
}

function actionLabel(action: string): string {
  if (action.startsWith('upgrade:')) return `Upgrade to ${action.slice('upgrade:'.length)}`
  return ACTION_LABELS[action] ?? action
}
function copy(text: string) {
  void navigator.clipboard?.writeText(text)
}

function fmtDate(t: string | null): string {
  if (!t) return '—'
  const d = new Date(t)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString()
}

function scopeLabel(scope: MCPScope): string {
  return SCOPE_LABELS[scope] ?? scope
}

/** A labelled, copyable command block. */
function Snippet({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      <div className="flex items-start gap-2">
        <code className="flex-1 whitespace-pre-wrap break-all rounded bg-surface-2 px-3 py-2 font-mono text-xs text-text-primary">
          {value}
        </code>
        <Button
          variant="outline"
          title={`Copy ${label}`}
          aria-label={`Copy ${label}`}
          onClick={() => {
            copy(value)
            setCopied(true)
            window.setTimeout(() => setCopied(false), 1500)
          }}
        >
          {copied ? (
            <Check className="h-4 w-4 text-green-400" />
          ) : (
            <Copy className="h-4 w-4" />
          )}
        </Button>
      </div>
    </div>
  )
}

function ConnectPanel({ info }: { info: MCPConnectionInfo }) {
  if (!info.configured) {
    return (
      <div className="flex items-start gap-2 rounded-md border border-border bg-surface-2/40 p-3 text-xs text-text-secondary">
        <ShieldAlert className="mt-0.5 h-4 w-4 flex-shrink-0 text-amber-400" />
        <span>
          Remote agent access is off because this deployment has no public
          address configured. Set <code className="font-mono">MCP_PUBLIC_ORIGIN</code>{' '}
          (or <code className="font-mono">APP_ORIGIN</code>) to the HTTPS origin
          clients should reach, then reload.
        </span>
      </div>
    )
  }

  return (
    <div className="space-y-3 rounded-md border border-border bg-surface-2/40 p-4">
      <p className="text-xs text-text-secondary">
        Run this once on the machine the agent runs on. Your browser opens, you
        pick the servers and capabilities, and the agent is connected — there is
        no token to copy, and nothing secret in these commands.
      </p>
      {/* Labelled "… command"/"… config" rather than by tool name alone: a
          connected client picks its own display name, and a grant called
          "Claude Code" sitting under a heading called "Claude Code" reads as
          the same thing twice. */}
      <Snippet label="Claude Code command" value={info.claude_code} />
      <Snippet label="Codex config (config.toml)" value={info.codex_config} />
      <Snippet label="Hermes config (~/.hermes/config.yaml)" value={info.hermes_config} />
      <Snippet label="Server URL" value={info.url} />
    </div>
  )
}

function ActionRow({ request }: { request: MCPActionRequest }) {
  const qc = useQueryClient()
  const { success, error } = useNotifications()
  const [approving, setApproving] = useState(false)
  const [password, setPassword] = useState('')
  const [totp, setTotp] = useState('')

  const mfaEnabled = useMfaEnabled()

  const reset = () => {
    setApproving(false)
    setPassword('')
    setTotp('')
  }

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['mcp-action-requests'] })
    // An executed action changes what the rest of the panel shows.
    qc.invalidateQueries({ queryKey: ['servers'] })
  }

  const approve = useMutation({
    mutationFn: () =>
      api.mcp.actionRequests.approve(
        request.id,
        // Sending nothing when no step-up is in force, rather than an empty
        // password: the API counts an empty password as a failed attempt, and
        // spending a throttle slot on a request that needs no credential would
        // eventually lock the owner out of their own approvals.
        request.requires_password
          ? { password, totp_code: totp.trim() || undefined }
          : undefined,
      ),
    onSuccess: (settled) => {
      reset()
      refresh()
      if (settled.status === 'failed') {
        error('The action failed', settled.failure_reason ?? 'The server did not accept it.')
        return
      }
      success(`${actionLabel(request.action)} approved`, 'The workflow started once.')
    },
    onError: (e: Error) => error('Could not approve', e.message),
  })

  const deny = useMutation({
    mutationFn: () => api.mcp.actionRequests.deny(request.id),
    onSuccess: () => {
      reset()
      refresh()
      success('Request refused')
    },
    onError: (e: Error) => error('Could not refuse', e.message),
  })

  const pending = request.status === 'pending'
  const ready = stepUpReady(password, totp, mfaEnabled)

  return (
    <div className="space-y-2 py-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-text-primary">
              {actionLabel(request.action)} {request.server_name}
            </span>
            <StatusBadge status={request.status} />
          </div>
          <div className="text-xs text-text-secondary">
            asked by {request.client_name} · {fmtDate(request.created_at)}
            {pending ? ` · expires ${fmtDate(request.expires_at)}` : ''}
          </div>

          {request.action.startsWith('upgrade:') && (
            <div className="text-xs text-amber-300">
              Approval creates one full restore-point backup, changes the server version and mods,
              watches the boot, and automatically restores the backup if startup is unhealthy.
            </div>
          )}
          {request.failure_reason && (
            <div className="text-xs text-red-400">{request.failure_reason}</div>
          )}
        </div>

        {pending && !approving && (
          <div className="flex shrink-0 gap-2">
            {/* With no step-up in force this is the whole interaction; with one,
                it opens the confirmation panel below. */}
            <Button
              size="sm"
              onClick={() =>
                request.requires_password ? setApproving(true) : approve.mutate()
              }
              loading={!request.requires_password && approve.isPending}
            >
              Approve
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => deny.mutate()}
              loading={deny.isPending}
            >
              Refuse
            </Button>
          </div>
        )}
      </div>

      {/* The agent wrote this. It is shown so a human can judge whether the
          stated reason matches what they can see for themselves — never as an
          instruction, and never interpreted by the panel. */}
      {request.reason && (
        <div className="rounded-md border border-border bg-surface-2/40 p-3">
          <div className="mb-1 flex items-center gap-1.5 text-[10px] uppercase tracking-wide text-text-secondary">
            <ShieldAlert className="h-3 w-3 text-amber-400" />
            Agent's stated reason — untrusted text
          </div>
          <p className="whitespace-pre-wrap break-words text-xs text-text-secondary">
            {request.reason}
          </p>
        </div>
      )}

      {approving && (
        <div className="space-y-2 rounded-md border border-border bg-surface-2/40 p-3">
          <p className="text-xs text-text-secondary">
            Confirm it's you. This runs the action once, right now, and re-checks
            that the connection still has the access it needs.
          </p>
          <StepUpFields
            idPrefix={`approve-${request.id}`}
            password={password}
            totp={totp}
            onPassword={setPassword}
            onTotp={setTotp}
            mfaEnabled={mfaEnabled}
            onSubmit={() => ready && approve.mutate()}
          />
          <div className="flex gap-2">
            <Button
              size="sm"
              onClick={() => approve.mutate()}
              loading={approve.isPending}
              disabled={!ready}
            >
              Approve and run
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

function StatusBadge({ status }: { status: string }) {
  const cls =
    status === 'pending'
      ? 'border-amber-500/30 bg-amber-500/15 text-amber-300'
      : status === 'executed'
        ? 'border-green-500/30 bg-green-500/15 text-green-400'
        : status === 'failed'
          ? 'border-red-500/30 bg-red-500/15 text-red-400'
          : 'border-border bg-surface-2 text-text-secondary'
  const label =
    status === 'executed'
      ? 'Ran'
      : status === 'pending'
        ? 'Waiting for you'
        : status.charAt(0).toUpperCase() + status.slice(1)
  return <span className={`rounded border px-1.5 py-0.5 text-[10px] ${cls}`}>{label}</span>
}

function GrantRow({ grant }: { grant: MCPGrant }) {
  const qc = useQueryClient()
  const { success, error } = useNotifications()
  const [confirming, setConfirming] = useState(false)
  const [tuning, setTuning] = useState(false)

  const revoke = useMutation({
    mutationFn: () => api.mcp.grants.revoke(grant.id),
    onSuccess: () => {
      setConfirming(false)
      qc.invalidateQueries({ queryKey: ['mcp-grants'] })
      success('Connection revoked', 'The agent stops working immediately.')
    },
    onError: (e: Error) => error('Could not revoke', e.message),
  })

  const expired = new Date(grant.expires_at).getTime() <= Date.now()
  const state = grant.revoked_at
    ? { label: 'Revoked', className: 'border-border bg-surface-2 text-text-secondary' }
    : expired
      ? { label: 'Expired', className: 'border-border bg-surface-2 text-text-secondary' }
      : { label: 'Active', className: 'border-green-500/30 bg-green-500/15 text-green-400' }

  return (
    <div className="space-y-2 py-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-text-primary">
              {grant.client_name}
            </span>
            <span className={`rounded border px-1.5 py-0.5 text-[10px] ${state.className}`}>
              {state.label}
            </span>
          </div>
          <div className="text-xs text-text-secondary">
            {grant.servers.map((s) => s.name || s.id).join(', ') || 'no servers'}
          </div>
          <div className="flex flex-wrap gap-1">
            {grant.scopes.map((s) => (
              <span
                key={s}
                title={
                  EXECUTING_SCOPES.has(s)
                    ? 'This agent can do this on its own, without asking you first.'
                    : undefined
                }
                className={`rounded border px-1.5 py-0.5 text-[10px] ${
                  EXECUTING_SCOPES.has(s)
                    ? 'border-amber-500/30 bg-amber-500/15 text-amber-300'
                    : 'border-border bg-surface-2 text-text-secondary'
                }`}
              >
                {scopeLabel(s)}
              </span>
            ))}
          </div>
          {grant.scopes.some((s) => EXECUTING_SCOPES.has(s)) && !grant.revoked_at && (
            <div className="text-xs text-amber-300">
              Highlighted capabilities run without asking you. Revoke this
              connection to stop them immediately.
            </div>
          )}
          <div className="text-xs text-text-secondary">
            Connected {fmtDate(grant.created_at)} · expires {fmtDate(grant.expires_at)} ·
            last used {fmtDate(grant.last_used_at)}
            {grant.last_used_ip ? ` from ${grant.last_used_ip}` : ''}
            {grant.revoked_at ? ` · revoked ${fmtDate(grant.revoked_at)}` : ''}
          </div>
        </div>

        {grant.active && !confirming && (
          <div className="flex shrink-0 gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setTuning((v) => !v)}
              aria-expanded={tuning}
            >
              <SlidersHorizontal className="h-3.5 w-3.5" />
              Approvals
            </Button>
            <Button variant="outline" size="sm" onClick={() => setConfirming(true)}>
              <Trash2 className="h-3.5 w-3.5 text-red-400" />
              Revoke
            </Button>
          </div>
        )}
      </div>

      {grant.active && tuning && <GrantPolicyPanel grant={grant} />}

      {confirming && (
        <div className="space-y-2 rounded-md border border-red-500/30 bg-red-500/10 p-3">
          <p className="text-xs text-text-secondary">
            The agent loses access on its very next call — no password needed to
            stop it. Reconnecting means running the connect command again.
          </p>
          <div className="flex gap-2">
            <Button
              variant="destructive"
              size="sm"
              onClick={() => revoke.mutate()}
              loading={revoke.isPending}
            >
              Revoke connection
            </Button>
            <Button variant="outline" size="sm" onClick={() => setConfirming(false)}>
              Cancel
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

// RemoteAgentsCard is the owner's view of delegated agent access: how to
// connect one, what is connected now, and the actions an agent has asked
// permission to take.
export function RemoteAgentsCard() {
  const { data: info } = useQuery({
    queryKey: ['mcp-connection'],
    queryFn: api.mcp.connection,
  })
  const { data: grants = [], isLoading } = useQuery({
    queryKey: ['mcp-grants'],
    queryFn: api.mcp.grants.list,
  })
  const { data: requests = [] } = useQuery({
    queryKey: ['mcp-action-requests'],
    queryFn: api.mcp.actionRequests.list,
    // A request expires in 15 minutes and the agent is waiting on it, so this
    // list is the one thing here that has to stay fresh on its own.
    refetchInterval: 15_000,
  })

  const pending = requests.filter((r) => r.status === 'pending')
  const settled = requests.filter((r) => r.status !== 'pending').slice(0, 5)

  return (
    <div className="space-y-4 rounded-lg border border-border bg-surface p-5">
      <div className="flex items-start gap-3">
        <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-accent/10">
          <Plug className="h-4 w-4 text-accent" />
        </div>
        <div>
          <h3 className="font-semibold text-text-primary">Remote agent connections</h3>
          <p className="mt-1 text-sm text-text-secondary">
            Let an AI coding agent diagnose — and, if you choose, operate — your
            servers over MCP. You approve each connection in the browser, and no
            token is ever copied into a config file. A connection starts as
            read-only; anything beyond that is a capability you tick yourself.
            Operator capabilities run without a further prompt, so check the
            highlighted badges below and revoke anything you no longer supervise.
          </p>
        </div>
      </div>

      {info && <ConnectPanel info={info} />}

      <ApprovalPolicyCard />

      {pending.length > 0 && (
        <div className="space-y-1 rounded-md border border-amber-500/30 bg-amber-500/5 p-3">
          <h4 className="text-sm font-semibold text-text-primary">
            Waiting for your approval
          </h4>
          <div className="divide-y divide-border">
            {pending.map((r) => (
              <ActionRow key={r.id} request={r} />
            ))}
          </div>
        </div>
      )}

      <div className="space-y-1">
        <h4 className="text-sm font-semibold text-text-primary">Connections</h4>
        {isLoading ? (
          <div className="flex justify-center py-6">
            <div className="h-5 w-5 animate-spin rounded-full border-2 border-accent border-t-transparent" />
          </div>
        ) : grants.length === 0 ? (
          <p className="text-sm text-text-secondary">
            Nothing connected yet. Run the command above on the machine your
            agent runs on.
          </p>
        ) : (
          <div className="divide-y divide-border">
            {grants.map((g) => (
              <GrantRow key={g.id} grant={g} />
            ))}
          </div>
        )}
      </div>

      {settled.length > 0 && (
        <div className="space-y-1">
          <h4 className="text-sm font-semibold text-text-primary">Recent action requests</h4>
          <div className="divide-y divide-border">
            {settled.map((r) => (
              <ActionRow key={r.id} request={r} />
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
