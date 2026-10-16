import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError, onSessionEnded } from '@/lib/api'
import { reconnectLive } from '@/live/events'
import { useAuthStore } from '@/store/auth'
import { Btn, Field, TextBtn } from '@/ui/controls'
import { failureOf } from '@/ui/failure'
import { Dialog } from '@/ui/overlays'
import { Avatar } from '@/ui/pixel'
import { setUi, useUi } from '@/ui/store'

/** While the panel is on screen, an ended session asks to sign in again over
 *  the page instead of leaving it. */
export function useSessionEndedPrompt(active: boolean) {
  useEffect(() => {
    if (!active) return
    onSessionEnded(() => {
      // Several requests can fail at once; one prompt is enough.
      if (!useUi.getState().reauth) setUi({ reauth: { label: null, retry: null } })
    })
    return () => onSessionEnded(null)
  }, [active])
}

/** The session ended. Sign in again on top of the page, so nothing on it is
 *  lost, then carry on with whatever was being done. */
export function ReauthHost() {
  const r = useUi((s) => s.reauth)
  const user = useAuthStore((s) => s.user)
  const login = useAuthStore((s) => s.login)
  const logout = useAuthStore((s) => s.logout)
  const qc = useQueryClient()
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [needCode, setNeedCode] = useState(false)
  const [error, setError] = useState<{ pw?: string; code?: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const open = !!r
  useEffect(() => {
    setPassword('')
    setCode('')
    setNeedCode(false)
    setError(null)
    setBusy(false)
  }, [open])
  if (!r || !user) return null

  const go = async () => {
    if (!password) return setError({ pw: 'Enter your password.' })
    if (needCode && !/^\d{6}$/.test(code)) return setError({ code: 'The code is six digits.' })
    setError(null)
    setBusy(true)
    try {
      const res = await login(user.email, password, needCode ? { totpCode: code } : undefined)
      if (res.mfaRequired) {
        setNeedCode(true)
        setBusy(false)
        return
      }
    } catch (e) {
      setBusy(false)
      const wrong = e instanceof ApiError && e.status === 401
      if (wrong && needCode) setError({ code: 'That code didn’t work. Codes change every 30 seconds.' })
      else if (wrong) setError({ pw: 'That isn’t your password.' })
      else setError({ [needCode ? 'code' : 'pw']: failureOf(e).message })
      return
    }
    setUi({ reauth: null })
    void qc.invalidateQueries()
    reconnectLive()
    if (r.retry) setTimeout(r.retry, 50)
  }

  return (
    <Dialog
      open
      onClose={() => setUi({ reauth: null })}
      title="Sign in again"
      foot={
        <>
          <TextBtn
            quiet
            className="left"
            onClick={() => {
              setUi({ reauth: null })
              void logout()
            }}
          >
            Use a different account
          </TextBtn>
          <Btn variant="primary" loading={busy} onClick={() => void go()}>
            {r.label ? 'Sign in and ' + r.label : 'Sign in'}
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 16 }}>
        <p className="t2">
          Your session ended{r.label ? `, so the panel couldn’t ${r.label}` : ''}. That happens after a while signed in, or when it’s signed out from another device. You’ll stay on this page and nothing on it is lost.
        </p>
        <div className="row" style={{ gap: 10 }}>
          <Avatar name={user.display_name || user.email} size={26} />
          <span>{user.email}</span>
        </div>
        <div className={needCode ? 'stepup two' : 'stepup'}>
          <Field label="Your password" error={error?.pw}>
            <input
              className="input"
              type="password"
              value={password}
              autoFocus
              autoComplete="current-password"
              onChange={(e) => {
                setPassword(e.target.value)
                setError(null)
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void go()
              }}
            />
          </Field>
          {needCode && (
            <Field label="Two-factor code" error={error?.code}>
              <input
                className="input num"
                inputMode="numeric"
                maxLength={6}
                placeholder="000000"
                value={code}
                autoFocus
                autoComplete="one-time-code"
                onChange={(e) => {
                  setCode(e.target.value.replace(/\D/g, ''))
                  setError(null)
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void go()
                }}
              />
            </Field>
          )}
        </div>
      </div>
    </Dialog>
  )
}
