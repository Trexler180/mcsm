import { createRoute, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { Route as rootRoute } from './__root'
import { ApiError } from '@/lib/api'
import { useAuthStore } from '@/store/auth'
import { returnToHref, takeReturnTo } from '@/lib/return-to'
import { Btn, Field, TextBtn } from '@/ui/controls'
import { failureOf } from '@/ui/failure'
import { BrandMark } from '@/ui/icon'

function LoginPage() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [step, setStep] = useState<'password' | 'code'>('password')
  const [useRecovery, setUseRecovery] = useState(false)
  const [code, setCode] = useState('')
  const { login } = useAuthStore()
  const navigate = useNavigate()

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    if (step === 'password' && !password) return setError('Enter your password.')
    if (step === 'code' && !useRecovery && !/^\d{6}$/.test(code)) return setError('The code is six digits.')
    setLoading(true)
    try {
      const opts = step === 'code' ? (useRecovery ? { recoveryCode: code.trim() } : { totpCode: code }) : undefined
      const res = await login(email, password, opts)
      if (res.mfaRequired) {
        // Password accepted; the second factor is next.
        setStep('code')
        setLoading(false)
        return
      }
      // A remembered destination (the OAuth consent screen, say) is a plain
      // internal path rather than one of the router's literal routes, so it
      // goes through the browser. The session is already in storage, so the
      // reload comes back signed in.
      const back = takeReturnTo()
      if (back) {
        window.location.assign(returnToHref(back))
        return
      }
      void navigate({ to: '/' })
    } catch (err) {
      const wrong = err instanceof ApiError && err.status === 401
      setError(wrong ? (step === 'code' ? 'That code didn’t work.' : 'That email and password don’t match.') : failureOf(err).message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="login">
      <form className="login-box" onSubmit={submit} noValidate>
        <div className="brand" style={{ padding: 0, marginBottom: 8 }}>
          <BrandMark />
          MCSM
        </div>
        {step === 'password' ? (
          <>
            <Field label="Email">
              <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="username" autoFocus required />
            </Field>
            <Field label="Password" error={error}>
              <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />
            </Field>
            <Btn type="submit" variant="primary" loading={loading}>
              Sign in
            </Btn>
          </>
        ) : (
          <>
            <p className="t2">{useRecovery ? 'Enter one of the recovery codes you saved when you turned on two-factor sign-in.' : 'Enter the six-digit code from your authenticator app.'}</p>
            <Field error={error}>
              {useRecovery ? (
                <input className="input mono" value={code} onChange={(e) => setCode(e.target.value)} autoFocus autoComplete="one-time-code" placeholder="xxxx-xxxx-xxxx-xxxx" aria-label="Recovery code" />
              ) : (
                <input
                  className="input num"
                  inputMode="numeric"
                  maxLength={6}
                  value={code}
                  onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
                  autoFocus
                  autoComplete="one-time-code"
                  placeholder="000000"
                  aria-label="Two-factor code"
                  style={{ fontSize: 20, letterSpacing: '.25em', height: 42 }}
                />
              )}
            </Field>
            <Btn type="submit" variant="primary" loading={loading}>
              Continue
            </Btn>
            <TextBtn
              quiet
              onClick={() => {
                setUseRecovery((v) => !v)
                setCode('')
                setError('')
              }}
            >
              {useRecovery ? 'Use an authenticator code instead' : 'Can’t use your authenticator? Use a recovery code'}
            </TextBtn>
            <TextBtn
              quiet
              onClick={() => {
                setStep('password')
                setCode('')
                setError('')
                setPassword('')
              }}
            >
              Use a different account
            </TextBtn>
          </>
        )}
      </form>
    </div>
  )
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: LoginPage,
})
