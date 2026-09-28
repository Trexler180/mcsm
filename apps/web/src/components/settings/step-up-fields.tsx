import { useQuery } from '@tanstack/react-query'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'

// The password (+ TOTP when enrolled) pair asked for whenever an already-signed-in
// person has to re-prove it is them. Extracted because three surfaces need it —
// the approval card, the global approval prompt, and the policy toggles that
// relax the gate — and three copies would be three chances for one of them to
// forget the TOTP field on an MFA account.

/** Whether the signed-in account has TOTP enrolled, and so whether a code is
 *  part of the step-up at all. */
export function useMfaEnabled(): boolean {
  const { data } = useQuery({ queryKey: ['mfa-status'], queryFn: api.auth.mfa.status })
  return Boolean(data?.enabled)
}

/** Whether what has been typed is worth submitting. Purely a UI gate to avoid a
 *  round trip that is certain to 401 — the API is the authority, and it counts
 *  an empty password as a failed attempt rather than a free probe. */
export function stepUpReady(password: string, totp: string, mfaEnabled: boolean): boolean {
  return password.length > 0 && (!mfaEnabled || totp.trim().length >= 6)
}

interface StepUpFieldsProps {
  /** Distinguishes the inputs when more than one of these is on the page. */
  idPrefix: string
  password: string
  totp: string
  onPassword: (v: string) => void
  onTotp: (v: string) => void
  mfaEnabled: boolean
  /** Submit on Enter, so the common case never needs the mouse. */
  onSubmit?: () => void
}

export function StepUpFields({
  idPrefix,
  password,
  totp,
  onPassword,
  onTotp,
  mfaEnabled,
  onSubmit,
}: StepUpFieldsProps) {
  const submitOnEnter = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && onSubmit) onSubmit()
  }
  return (
    <div className="grid gap-2 sm:grid-cols-2">
      <div className="space-y-1.5">
        <Label htmlFor={`${idPrefix}-pw`}>Confirm your password</Label>
        <Input
          id={`${idPrefix}-pw`}
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => onPassword(e.target.value)}
          onKeyDown={submitOnEnter}
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
            onChange={(e) => onTotp(e.target.value)}
            onKeyDown={submitOnEnter}
          />
        </div>
      )}
    </div>
  )
}
