import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ShieldAlert } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { StepUpFields, stepUpReady, useMfaEnabled } from './step-up-fields'
import { api } from '@/lib/api'
import { useNotifications } from '@/store/notifications'
import type { MCPApprovalPolicy, MCPGrant, MCPGrantPolicyOverrides } from '@/lib/types'

// How much ceremony an agent's request goes through, at two layers: the account
// default, and a per-connection override that can point either way.
//
// The rule that shapes this UI is that relaxing needs a step-up and tightening
// does not. So there is no "Save" that always asks for a password — the form
// works out whether what you changed weakens anything, and only then asks.

const SECURE: MCPApprovalPolicy = {
  require_password: true,
  auto_approve_lifecycle: false,
  auto_approve_upgrades: false,
}

/** Whether moving from `from` to `to` weakens the gate in any dimension. Mirrors
 *  ApprovalPolicy.Relaxes on the server, which is the one that actually decides
 *  — this copy only chooses whether to show the password fields. */
function relaxes(from: MCPApprovalPolicy, to: MCPApprovalPolicy): boolean {
  return (
    (from.require_password && !to.require_password) ||
    (!from.auto_approve_lifecycle && to.auto_approve_lifecycle) ||
    (!from.auto_approve_upgrades && to.auto_approve_upgrades)
  )
}

function samePolicy(a: MCPApprovalPolicy, b: MCPApprovalPolicy): boolean {
  return (
    a.require_password === b.require_password &&
    a.auto_approve_lifecycle === b.auto_approve_lifecycle &&
    a.auto_approve_upgrades === b.auto_approve_upgrades
  )
}

interface ToggleProps {
  id: string
  label: string
  description: string
  checked: boolean
  onChange: (v: boolean) => void
  /** Rendered when the toggle is on, in amber. Reserved for the settings whose
   *  cost is not obvious from the label. */
  warning?: string
}

function Toggle({ id, label, description, checked, onChange, warning }: ToggleProps) {
  return (
    <label htmlFor={id} className="flex cursor-pointer items-start gap-3">
      <input
        id={id}
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-0.5 h-4 w-4 flex-shrink-0 accent-accent"
      />
      <span className="min-w-0">
        <span className="block text-sm text-text-primary">{label}</span>
        <span className="block text-xs text-text-secondary">{description}</span>
        {checked && warning && (
          <span className="mt-1 flex items-start gap-1.5 text-xs text-amber-300">
            <ShieldAlert className="mt-0.5 h-3 w-3 flex-shrink-0" />
            {warning}
          </span>
        )}
      </span>
    </label>
  )
}

const LIFECYCLE_WARNING =
  'The agent can start, stop, and restart these servers on its own, with nobody asked. ' +
  'It can restart during a session you cannot see.'

const UPGRADE_WARNING =
  'The agent can change the Minecraft version unattended: reinstalling the runtime, ' +
  'rewriting every managed mod, and rolling the world back to a restore point if the boot fails. ' +
  'Only turn this on for a connection you actively supervise.'

const NO_PASSWORD_WARNING =
  'Anyone with access to a signed-in browser can approve an agent request with one click.'

/** The three toggles, shared by both layers. */
function PolicyToggles({
  idPrefix,
  value,
  onChange,
  disabled,
}: {
  idPrefix: string
  value: MCPApprovalPolicy
  onChange: (next: MCPApprovalPolicy) => void
  disabled?: boolean
}) {
  return (
    <fieldset disabled={disabled} className="space-y-3">
      <Toggle
        id={`${idPrefix}-pw`}
        label="Ask for my password before approving"
        description="Approving a request re-checks that it's you before anything runs."
        checked={value.require_password}
        onChange={(v) => onChange({ ...value, require_password: v })}
      />
      {!value.require_password && (
        <p className="flex items-start gap-1.5 pl-7 text-xs text-amber-300">
          <ShieldAlert className="mt-0.5 h-3 w-3 flex-shrink-0" />
          {NO_PASSWORD_WARNING}
        </p>
      )}
      <Toggle
        id={`${idPrefix}-life`}
        label="Approve start, stop, and restart automatically"
        description="Lifecycle requests run the moment the agent asks. You still get a notification afterwards."
        checked={value.auto_approve_lifecycle}
        onChange={(v) => onChange({ ...value, auto_approve_lifecycle: v })}
        warning={LIFECYCLE_WARNING}
      />
      <Toggle
        id={`${idPrefix}-upgrade`}
        label="Approve version upgrades automatically"
        description="Version-change requests run the moment the agent asks."
        checked={value.auto_approve_upgrades}
        onChange={(v) => onChange({ ...value, auto_approve_upgrades: v })}
        warning={UPGRADE_WARNING}
      />
    </fieldset>
  )
}

/** Account-level defaults, behind every connection with no override of its own. */
export function ApprovalPolicyCard() {
  const qc = useQueryClient()
  const { success, error } = useNotifications()
  const mfaEnabled = useMfaEnabled()
  const [draft, setDraft] = useState<MCPApprovalPolicy | null>(null)
  const [password, setPassword] = useState('')
  const [totp, setTotp] = useState('')

  const { data: saved } = useQuery({
    queryKey: ['mcp-approval-settings'],
    queryFn: api.mcp.approvalSettings.get,
  })
  const current = saved ?? SECURE
  const value = draft ?? current

  const reset = () => {
    setDraft(null)
    setPassword('')
    setTotp('')
  }

  const save = useMutation({
    mutationFn: () =>
      api.mcp.approvalSettings.set({
        ...value,
        password: password || undefined,
        totp_code: totp.trim() || undefined,
      }),
    onSuccess: () => {
      reset()
      qc.invalidateQueries({ queryKey: ['mcp-approval-settings'] })
      // Resolved policy is echoed on every connection and every request.
      qc.invalidateQueries({ queryKey: ['mcp-grants'] })
      qc.invalidateQueries({ queryKey: ['mcp-action-requests'] })
      success('Approval settings saved')
    },
    onError: (e: Error) => error('Could not save', e.message),
  })

  const dirty = !samePolicy(value, current)
  const needsStepUp = relaxes(current, value)
  const canSave = dirty && (!needsStepUp || stepUpReady(password, totp, mfaEnabled))

  return (
    <div className="space-y-3 rounded-md border border-border bg-surface-2/40 p-4">
      <div>
        <h4 className="text-sm font-semibold text-text-primary">
          What an agent has to go through
        </h4>
        <p className="mt-1 text-xs text-text-secondary">
          The default for every connection. A connection can override any of
          these for itself.
        </p>
      </div>

      <PolicyToggles idPrefix="mcp-policy" value={value} onChange={setDraft} />

      {needsStepUp && (
        <div className="space-y-2 rounded-md border border-amber-500/30 bg-amber-500/5 p-3">
          <p className="text-xs text-text-secondary">
            You're removing a protection, so confirm it's you. Turning one back
            on never asks.
          </p>
          {/* Distinct from the toggles' own prefix: "mcp-policy-pw" would
              otherwise be both the require-password checkbox and this field. */}
          <StepUpFields
            idPrefix="mcp-policy-stepup"
            password={password}
            totp={totp}
            onPassword={setPassword}
            onTotp={setTotp}
            mfaEnabled={mfaEnabled}
            onSubmit={() => canSave && save.mutate()}
          />
        </div>
      )}

      {dirty && (
        <div className="flex gap-2">
          <Button size="sm" onClick={() => save.mutate()} loading={save.isPending} disabled={!canSave}>
            Save
          </Button>
          <Button variant="outline" size="sm" onClick={reset}>
            Cancel
          </Button>
        </div>
      )}
    </div>
  )
}

/** Per-connection overrides. Each toggle is tri-state; "Use the default" is the
 *  third state and is what every connection starts in. */
export function GrantPolicyPanel({ grant }: { grant: MCPGrant }) {
  const qc = useQueryClient()
  const { success, error } = useNotifications()
  const mfaEnabled = useMfaEnabled()
  const [draft, setDraft] = useState<MCPGrantPolicyOverrides | null>(null)
  const [password, setPassword] = useState('')
  const [totp, setTotp] = useState('')

  const overrides = draft ?? grant.overrides
  const reset = () => {
    setDraft(null)
    setPassword('')
    setTotp('')
  }

  const save = useMutation({
    mutationFn: () =>
      api.mcp.grants.setApproval(grant.id, {
        ...overrides,
        password: password || undefined,
        totp_code: totp.trim() || undefined,
      }),
    onSuccess: () => {
      reset()
      qc.invalidateQueries({ queryKey: ['mcp-grants'] })
      qc.invalidateQueries({ queryKey: ['mcp-action-requests'] })
      success('Connection settings saved')
    },
    onError: (e: Error) => error('Could not save', e.message),
  })

  // What the connection resolves to today versus what the draft would make it.
  // Both are computed against the account default the API already resolved into
  // `grant.policy`, so the inherit case reads correctly without a second fetch.
  const inherited: MCPApprovalPolicy = {
    require_password:
      grant.overrides.require_password ?? grant.policy.require_password,
    auto_approve_lifecycle:
      grant.overrides.auto_approve_lifecycle ?? grant.policy.auto_approve_lifecycle,
    auto_approve_upgrades:
      grant.overrides.auto_approve_upgrades ?? grant.policy.auto_approve_upgrades,
  }
  const next: MCPApprovalPolicy = {
    require_password: overrides.require_password ?? inherited.require_password,
    auto_approve_lifecycle:
      overrides.auto_approve_lifecycle ?? inherited.auto_approve_lifecycle,
    auto_approve_upgrades:
      overrides.auto_approve_upgrades ?? inherited.auto_approve_upgrades,
  }

  const dirty =
    overrides.require_password !== grant.overrides.require_password ||
    overrides.auto_approve_lifecycle !== grant.overrides.auto_approve_lifecycle ||
    overrides.auto_approve_upgrades !== grant.overrides.auto_approve_upgrades
  const needsStepUp = relaxes(grant.policy, next)
  const canSave = dirty && (!needsStepUp || stepUpReady(password, totp, mfaEnabled))

  const rows: {
    key: keyof MCPGrantPolicyOverrides
    label: string
    onLabel: string
    offLabel: string
  }[] = [
    {
      key: 'require_password',
      label: 'Password before approving',
      onLabel: 'Always ask',
      offLabel: 'Never ask',
    },
    {
      key: 'auto_approve_lifecycle',
      label: 'Start / stop / restart',
      onLabel: 'Run automatically',
      offLabel: 'Ask me',
    },
    {
      key: 'auto_approve_upgrades',
      label: 'Version upgrades',
      onLabel: 'Run automatically',
      offLabel: 'Ask me',
    },
  ]

  return (
    <div className="space-y-3 rounded-md border border-border bg-surface-2/40 p-3">
      <p className="text-xs text-text-secondary">
        Overrides for this connection only. Anything left on "Use the default"
        follows your account setting.
      </p>

      <div className="space-y-2">
        {rows.map((row) => (
          <div key={row.key} className="flex flex-wrap items-center justify-between gap-2">
            <span className="text-xs text-text-primary">{row.label}</span>
            <select
              aria-label={row.label}
              className="rounded border border-border bg-surface px-2 py-1 text-xs text-text-primary"
              value={
                overrides[row.key] === null || overrides[row.key] === undefined
                  ? 'inherit'
                  : overrides[row.key]
                    ? 'on'
                    : 'off'
              }
              onChange={(e) =>
                setDraft({
                  ...overrides,
                  [row.key]:
                    e.target.value === 'inherit' ? null : e.target.value === 'on',
                })
              }
            >
              <option value="inherit">Use the default</option>
              <option value="on">{row.onLabel}</option>
              <option value="off">{row.offLabel}</option>
            </select>
          </div>
        ))}
      </div>

      {(next.auto_approve_lifecycle || next.auto_approve_upgrades) && (
        <p className="flex items-start gap-1.5 text-xs text-amber-300">
          <ShieldAlert className="mt-0.5 h-3 w-3 flex-shrink-0" />
          {next.auto_approve_upgrades ? UPGRADE_WARNING : LIFECYCLE_WARNING}
        </p>
      )}

      {needsStepUp && (
        <div className="space-y-2 rounded-md border border-amber-500/30 bg-amber-500/5 p-3">
          <p className="text-xs text-text-secondary">
            You're removing a protection for this connection, so confirm it's you.
          </p>
          <StepUpFields
            idPrefix={`mcp-grant-${grant.id}`}
            password={password}
            totp={totp}
            onPassword={setPassword}
            onTotp={setTotp}
            mfaEnabled={mfaEnabled}
            onSubmit={() => canSave && save.mutate()}
          />
        </div>
      )}

      {dirty && (
        <div className="flex gap-2">
          <Button size="sm" onClick={() => save.mutate()} loading={save.isPending} disabled={!canSave}>
            Save
          </Button>
          <Button variant="outline" size="sm" onClick={reset}>
            Cancel
          </Button>
        </div>
      )}
    </div>
  )
}
