import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { QRCodeSVG } from 'qrcode.react'
import { ShieldCheck, ShieldOff, Monitor, Copy, LogOut } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import { useNotifications } from '@/store/notifications'
import type { MfaSetup, Session } from '@/lib/types'

function copy(text: string) {
  void navigator.clipboard?.writeText(text)
}

// MfaCard drives the full TOTP enrollment lifecycle: enable (setup → verify →
// show one-time recovery codes) and disable (confirm with a current code).
function MfaCard() {
  const qc = useQueryClient()
  const { success, error } = useNotifications()
  const { data: status } = useQuery({
    queryKey: ['mfa-status'],
    queryFn: api.auth.mfa.status,
  })

  const [setup, setSetup] = useState<MfaSetup | null>(null)
  const [code, setCode] = useState('')
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null)
  const [disabling, setDisabling] = useState(false)
  const [disableCode, setDisableCode] = useState('')

  const begin = useMutation({
    mutationFn: api.auth.mfa.setup,
    onSuccess: (s) => {
      setSetup(s)
      setRecoveryCodes(null)
    },
    onError: (e: Error) => error('Could not start MFA setup', e.message),
  })

  const enable = useMutation({
    mutationFn: () => api.auth.mfa.enable(code.trim()),
    onSuccess: (res) => {
      setRecoveryCodes(res.recovery_codes)
      setSetup(null)
      setCode('')
      qc.invalidateQueries({ queryKey: ['mfa-status'] })
      success('Two-factor authentication enabled')
    },
    onError: (e: Error) => error('Could not enable MFA', e.message),
  })

  const disable = useMutation({
    mutationFn: () => api.auth.mfa.disable({ code: disableCode.trim() }),
    onSuccess: () => {
      setDisabling(false)
      setDisableCode('')
      qc.invalidateQueries({ queryKey: ['mfa-status'] })
      success('Two-factor authentication disabled')
    },
    onError: (e: Error) => error('Could not disable MFA', e.message),
  })

  const enabled = status?.enabled

  return (
    <div className="rounded-lg border border-border bg-surface p-5 space-y-4">
      <div className="flex items-start gap-3">
        <div className="w-9 h-9 rounded-md bg-accent/10 flex items-center justify-center flex-shrink-0">
          {enabled ? (
            <ShieldCheck className="h-4 w-4 text-green-400" />
          ) : (
            <ShieldOff className="h-4 w-4 text-text-secondary" />
          )}
        </div>
        <div className="flex-1 min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="font-semibold text-text-primary">
              Two-factor authentication
            </h3>
            {enabled ? (
              <span className="text-xs px-2 py-0.5 rounded border border-green-500/30 bg-green-500/15 text-green-400">
                Enabled
              </span>
            ) : (
              <span className="text-xs px-2 py-0.5 rounded border border-border bg-surface-2 text-text-secondary">
                Off
              </span>
            )}
          </div>
          <p className="text-sm text-text-secondary mt-1">
            Require a time-based code from an authenticator app (Google
            Authenticator, 1Password, Aegis…) in addition to your password.
          </p>
        </div>
      </div>

      {/* Newly generated recovery codes — shown once. */}
      {recoveryCodes && (
        <div className="rounded-md border border-amber-500/30 bg-amber-500/10 p-4 space-y-2">
          <p className="text-sm text-amber-300 font-medium">
            Save your recovery codes
          </p>
          <p className="text-xs text-text-secondary">
            Each can be used once if you lose your authenticator. They won't be
            shown again.
          </p>
          <div className="grid grid-cols-2 gap-1.5 font-mono text-sm text-text-primary">
            {recoveryCodes.map((c) => (
              <span key={c}>{c}</span>
            ))}
          </div>
          <Button
            variant="outline"
            onClick={() => copy(recoveryCodes.join('\n'))}
          >
            <Copy className="h-4 w-4" />
            Copy codes
          </Button>
        </div>
      )}

      {/* Enroll flow. */}
      {!enabled && !setup && (
        <Button onClick={() => begin.mutate()} loading={begin.isPending}>
          Enable two-factor auth
        </Button>
      )}

      {!enabled && setup && (
        <div className="space-y-3">
          <div className="text-sm text-text-secondary">
            Scan this QR code with your authenticator app, then enter the 6-digit
            code it shows.
          </div>
          <div className="flex justify-center">
            {/* Rendered locally — the secret never leaves your browser. White
                quiet-zone padding so cameras scan it on the dark theme. */}
            <div className="rounded-lg bg-white p-3">
              <QRCodeSVG value={setup.otpauth_url} size={176} level="M" />
            </div>
          </div>
          <div className="text-xs text-text-secondary">
            Can't scan? Enter this key manually:
          </div>
          <div className="flex items-center gap-2">
            <code className="flex-1 break-all rounded bg-surface-2 px-3 py-2 font-mono text-sm text-text-primary">
              {setup.secret}
            </code>
            <Button variant="outline" onClick={() => copy(setup.secret)}>
              <Copy className="h-4 w-4" />
            </Button>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="mfa-code">Verification code</Label>
            <Input
              id="mfa-code"
              inputMode="numeric"
              autoComplete="one-time-code"
              placeholder="123456"
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
          </div>
          <div className="flex gap-2">
            <Button
              onClick={() => enable.mutate()}
              loading={enable.isPending}
              disabled={code.trim().length < 6}
            >
              Verify & enable
            </Button>
            <Button variant="outline" onClick={() => setSetup(null)}>
              Cancel
            </Button>
          </div>
        </div>
      )}

      {/* Disable flow. */}
      {enabled &&
        (disabling ? (
          <div className="space-y-2">
            <Label htmlFor="mfa-disable">
              Enter a current authentication code to confirm
            </Label>
            <div className="flex gap-2">
              <Input
                id="mfa-disable"
                inputMode="numeric"
                autoComplete="one-time-code"
                placeholder="123456"
                value={disableCode}
                onChange={(e) => setDisableCode(e.target.value)}
              />
              <Button
                variant="outline"
                onClick={() => disable.mutate()}
                loading={disable.isPending}
                disabled={disableCode.trim().length < 6}
              >
                Disable
              </Button>
            </div>
          </div>
        ) : (
          <Button variant="outline" onClick={() => setDisabling(true)}>
            Disable two-factor auth
          </Button>
        ))}
    </div>
  )
}

function deviceLabel(ua: string): string {
  if (!ua) return 'Unknown device'
  // A light touch — enough to tell sessions apart without a UA-parsing dep.
  const browser =
    /Edg/.test(ua) ? 'Edge'
    : /Chrome/.test(ua) ? 'Chrome'
    : /Firefox/.test(ua) ? 'Firefox'
    : /Safari/.test(ua) ? 'Safari'
    : 'Browser'
  const os =
    /Windows/.test(ua) ? 'Windows'
    : /Mac OS|Macintosh/.test(ua) ? 'macOS'
    : /Android/.test(ua) ? 'Android'
    : /iPhone|iPad|iOS/.test(ua) ? 'iOS'
    : /Linux/.test(ua) ? 'Linux'
    : ''
  return os ? `${browser} on ${os}` : browser
}

function fmtTime(t: string | null): string {
  if (!t) return '—'
  const d = new Date(t)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString()
}

function SessionsCard() {
  const qc = useQueryClient()
  const { success, error } = useNotifications()
  const { data: sessions = [], isLoading } = useQuery({
    queryKey: ['sessions'],
    queryFn: api.auth.sessions.list,
  })

  const revoke = useMutation({
    mutationFn: (id: string) => api.auth.sessions.revoke(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sessions'] })
      success('Session revoked')
    },
    onError: (e: Error) => error('Could not revoke session', e.message),
  })

  const revokeOthers = useMutation({
    mutationFn: api.auth.sessions.revokeOthers,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sessions'] })
      success('Signed out of all other sessions')
    },
    onError: (e: Error) => error('Could not sign out other sessions', e.message),
  })

  const hasOthers = sessions.some((s: Session) => !s.current)

  return (
    <div className="rounded-lg border border-border bg-surface p-5 space-y-4">
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <div className="w-9 h-9 rounded-md bg-accent/10 flex items-center justify-center flex-shrink-0">
            <Monitor className="h-4 w-4 text-accent" />
          </div>
          <div>
            <h3 className="font-semibold text-text-primary">Active sessions</h3>
            <p className="text-sm text-text-secondary mt-1">
              Devices currently signed in. Revoke any you don't recognize.
            </p>
          </div>
        </div>
        {hasOthers && (
          <Button
            variant="outline"
            onClick={() => revokeOthers.mutate()}
            loading={revokeOthers.isPending}
            className="shrink-0 whitespace-nowrap"
          >
            <LogOut className="h-4 w-4" />
            Sign out others
          </Button>
        )}
      </div>

      {isLoading ? (
        <div className="py-6 flex justify-center">
          <div className="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin" />
        </div>
      ) : (
        <div className="divide-y divide-border">
          {sessions.map((s: Session) => (
            <div
              key={s.id}
              className="flex items-center justify-between gap-3 py-3"
            >
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium text-text-primary truncate">
                    {deviceLabel(s.user_agent)}
                  </span>
                  {s.current && (
                    <span className="text-xs px-1.5 py-0.5 rounded border border-green-500/30 bg-green-500/15 text-green-400">
                      This device
                    </span>
                  )}
                </div>
                <div className="text-xs text-text-secondary mt-0.5">
                  {s.ip || 'unknown IP'} · last active {fmtTime(s.last_used_at)}
                </div>
              </div>
              {!s.current && (
                <Button
                  variant="outline"
                  onClick={() => revoke.mutate(s.id)}
                  loading={revoke.isPending && revoke.variables === s.id}
                >
                  Revoke
                </Button>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

export function SecuritySection() {
  return (
    <div className="space-y-4">
      <MfaCard />
      <SessionsCard />
    </div>
  )
}
