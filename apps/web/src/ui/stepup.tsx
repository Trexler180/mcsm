import { useEffect, useState, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { Btn, Field } from './controls'
import { Dialog } from './overlays'
import { setUi, useUi, type StepUpValue } from './store'
import { cx } from './util'

// Proving it's you again: the password, plus a two-factor code when the
// account has one. Asked whenever someone already signed in has to prove it's
// them: approving an agent, loosening approval rules, signing back in after
// the session ends. One component, so no surface forgets the code.

export interface StepUpErrors {
  pw?: string
  code?: string
}

/** Whether the signed-in account has two-factor sign-in turned on. */
export function useHasMfa(): boolean {
  const q = useQuery({ queryKey: ['mfa-status'], queryFn: () => api.auth.mfa.status(), staleTime: 60_000 })
  return !!q.data?.enabled
}

export function checkStepUp(v: StepUpValue, mfa: boolean): StepUpErrors | null {
  const e: StepUpErrors = {}
  if (!v.password) e.pw = 'Enter your password.'
  if (mfa && !/^\d{6}$/.test(v.code || '')) e.code = 'The code is six digits.'
  return Object.keys(e).length ? e : null
}

export function StepUpFields({
  value,
  onChange,
  errors,
  onEnter,
  mfa,
  autoFocus = true,
}: {
  value: StepUpValue
  onChange: (v: StepUpValue) => void
  errors?: StepUpErrors | null
  onEnter?: () => void
  mfa: boolean
  autoFocus?: boolean
}) {
  const key = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && onEnter) {
      e.preventDefault()
      onEnter()
    }
  }
  return (
    <div className={cx('stepup', mfa && 'two')}>
      <Field label="Your password" error={errors?.pw}>
        <input
          className="input"
          type="password"
          value={value.password}
          autoFocus={autoFocus}
          autoComplete="current-password"
          aria-label="Your password"
          onKeyDown={key}
          onChange={(e) => onChange({ ...value, password: e.target.value })}
        />
      </Field>
      {mfa && (
        <Field label="Two-factor code" error={errors?.code}>
          <input
            className="input num"
            inputMode="numeric"
            maxLength={6}
            placeholder="000000"
            value={value.code}
            autoComplete="one-time-code"
            aria-label="Two-factor code"
            onKeyDown={key}
            onChange={(e) => onChange({ ...value, code: e.target.value.replace(/\D/g, '') })}
          />
        </Field>
      )}
    </div>
  )
}

/** The dialog behind stepUp(). The caller sends what it resolves with to the
 *  API, which is what checks it. */
export function StepUpHost() {
  const c = useUi((s) => s.stepUp)
  const mfa = useHasMfa()
  const [v, setV] = useState<StepUpValue>({ password: '', code: '' })
  const [errors, setErrors] = useState<StepUpErrors | null>(null)
  useEffect(() => {
    setV({ password: '', code: '' })
    setErrors(null)
  }, [c])
  if (!c) return null
  const close = (ok: boolean) => {
    c.resolve(ok ? v : null)
    setUi({ stepUp: null })
  }
  const go = () => {
    const e = checkStepUp(v, mfa)
    setErrors(e)
    if (!e) close(true)
  }
  return (
    <Dialog
      open
      onClose={() => close(false)}
      title={c.title || 'Confirm it’s you'}
      foot={
        <>
          <Btn variant="quiet" onClick={() => close(false)}>
            Cancel
          </Btn>
          <Btn variant="primary" onClick={go}>
            {c.confirm || 'Confirm'}
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 16 }}>
        {c.body && <p className="t2">{c.body as ReactNode}</p>}
        <StepUpFields
          value={v}
          mfa={mfa}
          onChange={(x) => {
            setV(x)
            setErrors(null)
          }}
          errors={errors}
          onEnter={go}
        />
      </div>
    </Dialog>
  )
}
