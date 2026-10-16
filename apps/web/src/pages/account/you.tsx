import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { QRCodeSVG } from 'qrcode.react'
import { api } from '@/lib/api'
import type { MfaSetup, Session } from '@/lib/types'
import { useAuthStore } from '@/store/auth'
import { Btn, Empty, Field, IconBtn } from '@/ui/controls'
import { act } from '@/ui/failure'
import { relTime } from '@/ui/format'
import { Dialog } from '@/ui/overlays'
import { Loaded, SkItems } from '@/ui/skeleton'
import { ask, stepUp } from '@/ui/store'
import { useHasMfa } from '@/ui/stepup'
import { toast } from '@/ui/toasts'
import { copyText, toMs } from '@/ui/util'

// Who you are and how you sign in: your name, password, two-factor sign-in,
// and the places you're signed in.

/** "Firefox on Windows", from a user agent, without a parsing library. */
export function deviceLabel(ua: string): string {
  if (!ua) return 'An unknown device'
  const browser = /Edg\//.test(ua) ? 'Edge' : /OPR\/|Opera/.test(ua) ? 'Opera' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : /curl|python|Go-http/i.test(ua) ? 'A script' : 'A browser'
  const os = /Windows/.test(ua) ? 'Windows' : /iPhone|iPad|iOS/.test(ua) ? 'iOS' : /Mac OS|Macintosh/.test(ua) ? 'macOS' : /Android/.test(ua) ? 'Android' : /CrOS/.test(ua) ? 'ChromeOS' : /Linux/.test(ua) ? 'Linux' : ''
  return os ? `${browser} on ${os}` : browser
}

export function passwordError(next: string, again: string): string | null {
  if (next && next.length < 10) return 'Use at least 10 characters.'
  if (next.length > 72) return 'Keep it to 72 characters or fewer.'
  if (again && again !== next) return 'These don’t match.'
  return null
}

export function YouSection() {
  return (
    <div className="stack">
      <Profile />
      <Password />
      <TwoFactor />
      <Sessions />
    </div>
  )
}

function Profile() {
  const me = useAuthStore((s) => s.user)
  const [name, setName] = useState(me?.display_name ?? '')
  const [busy, setBusy] = useState(false)
  if (!me) return null
  const changed = name.trim() !== (me.display_name ?? '')
  const save = async () => {
    setBusy(true)
    const r = await act('save your name', () => api.auth.updateMe({ display_name: name.trim() }))
    setBusy(false)
    if (r === undefined) return
    useAuthStore.setState({ user: r })
    toast('ok', 'Saved your name')
  }
  return (
    <div className="form-section">
      <h2>Profile</h2>
      <div className="form-grid" style={{ marginTop: 12 }}>
        <Field label="Name" hint="Shown in the audit log and to other people on the panel.">
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} maxLength={64} placeholder={me.email.split('@')[0]} aria-label="Name" />
        </Field>
        <Field label="Email" hint="An administrator can change it.">
          <input className="input" value={me.email} readOnly aria-label="Email" />
        </Field>
      </div>
      {changed && (
        <div style={{ marginTop: 14 }}>
          <Btn loading={busy} onClick={() => void save()}>
            Save name
          </Btn>
        </div>
      )}
    </div>
  )
}

function Password() {
  const mfa = useHasMfa()
  const [pw, setPw] = useState({ cur: '', next: '', again: '', code: '' })
  const [busy, setBusy] = useState(false)
  const err = passwordError(pw.next, pw.again)
  const ready = pw.cur && pw.next && pw.again === pw.next && !err && (!mfa || /^\d{6}$/.test(pw.code))
  const change = async () => {
    setBusy(true)
    const r = await act('change your password', () => api.auth.changePassword({ current_password: pw.cur, new_password: pw.next, totp_code: mfa ? pw.code : undefined }))
    setBusy(false)
    if (r === undefined) return
    setPw({ cur: '', next: '', again: '', code: '' })
    toast('ok', 'Changed your password', 'You’ve been signed out everywhere else.')
  }
  return (
    <div className="form-section">
      <h2>Password</h2>
      <p className="lede">Changing it signs you out everywhere else.</p>
      <form
        className="form-grid"
        onSubmit={(e) => {
          e.preventDefault()
          if (ready) void change()
        }}
      >
        <Field label="Current password" className={mfa ? undefined : 'span'}>
          <input className="input" type="password" autoComplete="current-password" value={pw.cur} onChange={(e) => setPw({ ...pw, cur: e.target.value })} style={mfa ? undefined : { maxWidth: 340 }} aria-label="Current password" />
        </Field>
        {mfa && (
          <Field label="Two-factor code">
            <input className="input num" inputMode="numeric" maxLength={6} autoComplete="one-time-code" value={pw.code} onChange={(e) => setPw({ ...pw, code: e.target.value.replace(/\D/g, '') })} aria-label="Two-factor code" />
          </Field>
        )}
        <Field label="New password">
          <input className="input" type="password" autoComplete="new-password" value={pw.next} onChange={(e) => setPw({ ...pw, next: e.target.value })} aria-label="New password" />
        </Field>
        <Field label="Again" error={err}>
          <input className="input" type="password" autoComplete="new-password" value={pw.again} onChange={(e) => setPw({ ...pw, again: e.target.value })} aria-label="New password again" />
        </Field>
        <button type="submit" hidden />
      </form>
      <div style={{ marginTop: 14 }}>
        <Btn disabled={!ready} loading={busy} onClick={() => void change()}>
          Change password
        </Btn>
      </div>
    </div>
  )
}

function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  return (
    <Dialog open onClose={onDone} title="Save your recovery codes" foot={<Btn variant="primary" onClick={onDone}>I’ve saved them</Btn>}>
      <div className="stack" style={{ gap: 12 }}>
        <p className="t2">Each one signs you in once if you lose your authenticator. They aren’t shown again, so keep them somewhere safe, away from this device.</p>
        <div className="codeblock recovery-codes">
          {codes.map((c) => (
            <span key={c}>{c}</span>
          ))}
          <IconBtn
            icon="copy"
            label="Copy"
            onClick={() => {
              void copyText(codes.join('\n'))
              toast('ok', 'Copied the codes')
            }}
          />
        </div>
      </div>
    </Dialog>
  )
}

function TwoFactor() {
  const qc = useQueryClient()
  const status = useQuery({ queryKey: ['mfa-status'], queryFn: () => api.auth.mfa.status(), staleTime: 60_000 })
  const [setup, setSetup] = useState<MfaSetup | null>(null)
  const [codes, setCodes] = useState<string[] | null>(null)
  const refresh = () => void qc.invalidateQueries({ queryKey: ['mfa-status'] })

  const begin = async () => {
    const v = await stepUp({ title: 'Set up two-factor sign-in', body: 'Confirm your password first.', confirm: 'Continue' })
    if (!v) return
    const r = await act('start setting up two-factor', () => api.auth.mfa.setup(v.password))
    if (r) setSetup(r)
  }
  const newCodes = async () => {
    const v = await stepUp({ title: 'Make new recovery codes', body: 'Your old codes stop working.', confirm: 'Make new codes' })
    if (!v) return
    const r = await act('make new recovery codes', () => api.auth.mfa.regenerateRecoveryCodes(v.password, v.code))
    if (r) setCodes(r.recovery_codes)
  }
  const turnOff = async () => {
    if (!(await ask({ title: 'Turn off two-factor?', body: 'Anyone with your password could sign in as you.', confirm: 'Continue', danger: true }))) return
    const v = await stepUp({ title: 'Turn off two-factor', confirm: 'Turn off', body: 'Enter your password and a code from your authenticator.' })
    if (!v) return
    const r = await act('turn off two-factor', () => api.auth.mfa.disable({ password: v.password, code: v.code }))
    if (r === undefined) return
    toast('ok', 'Two-factor is off')
    refresh()
  }

  return (
    <div className="form-section">
      <h2>Two-factor sign-in</h2>
      <Loaded query={status} sk={<SkItems rows={1} />} what="your two-factor settings">
        {(s) =>
          s.enabled ? (
            <>
              <p className="lede">On. Signing in asks for a code from your authenticator app.</p>
              <div className="row" style={{ gap: 8 }}>
                <Btn onClick={() => void newCodes()}>New recovery codes</Btn>
                <Btn variant="quiet" onClick={() => void turnOff()}>
                  Turn off
                </Btn>
              </div>
            </>
          ) : (
            <>
              <p className="lede">Off. Anyone with your password can sign in as you.</p>
              <Btn variant="primary" onClick={() => void begin()}>
                Set it up
              </Btn>
            </>
          )
        }
      </Loaded>
      {setup && (
        <EnrollDialog
          setup={setup}
          onClose={() => setSetup(null)}
          onEnabled={(c) => {
            setSetup(null)
            setCodes(c)
            refresh()
            toast('ok', 'Two-factor is on')
          }}
        />
      )}
      {codes && <RecoveryCodes codes={codes} onDone={() => setCodes(null)} />}
    </div>
  )
}

function EnrollDialog({ setup, onClose, onEnabled }: { setup: MfaSetup; onClose: () => void; onEnabled: (codes: string[]) => void }) {
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const enable = async () => {
    if (code.length !== 6) return
    setBusy(true)
    const r = await act('turn on two-factor', () => api.auth.mfa.enable(code))
    setBusy(false)
    if (r) onEnabled(r.recovery_codes)
  }
  return (
    <Dialog
      open
      onClose={onClose}
      title="Set up two-factor sign-in"
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" disabled={code.length !== 6} loading={busy} onClick={() => void enable()}>
            Turn on
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 16 }}>
        <p className="t2">Scan this with an authenticator app (1Password, Aegis, Google Authenticator), then enter the six-digit code it shows.</p>
        <div className="row" style={{ gap: 18, alignItems: 'flex-start', flexWrap: 'wrap' }}>
          {/* Drawn here: the secret never leaves the browser. The white margin
              lets cameras find it on a dark page. */}
          <div className="qr">
            <QRCodeSVG value={setup.otpauth_url} size={168} level="M" marginSize={2} />
          </div>
          <div className="grow" style={{ minWidth: 200 }}>
            <Field label="Or type this key">
              <code className="mono small" style={{ wordBreak: 'break-all' }}>
                {setup.secret}
              </code>
            </Field>
          </div>
        </div>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void enable()
          }}
        >
          <Field label="Code">
            <input className="input num" inputMode="numeric" maxLength={6} autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))} autoFocus placeholder="000000" style={{ maxWidth: 160 }} aria-label="Code" />
          </Field>
        </form>
      </div>
    </Dialog>
  )
}

function Sessions() {
  const qc = useQueryClient()
  const sessions = useQuery({ queryKey: ['sessions'], queryFn: () => api.auth.sessions.list() })
  const refresh = () => void qc.invalidateQueries({ queryKey: ['sessions'] })
  const signOut = (s: Session) =>
    void act(`sign out ${deviceLabel(s.user_agent)}`, () => api.auth.sessions.revoke(s.id).then(() => true)).then((r) => {
      if (!r) return
      toast('ok', `Signed out ${deviceLabel(s.user_agent)}`)
      refresh()
    })
  const signOutOthers = async () => {
    if (!(await ask({ title: 'Sign out everywhere else?', body: 'Every other browser and device has to sign in again.', confirm: 'Sign out' }))) return
    const r = await act('sign out everywhere else', () => api.auth.sessions.revokeOthers().then(() => true))
    if (!r) return
    toast('ok', 'Signed out everywhere else')
    refresh()
  }
  return (
    <div className="form-section" style={{ borderBottom: 0 }}>
      <div className="row" style={{ alignItems: 'baseline' }}>
        <h2 className="grow">Signed in on</h2>
        {(sessions.data ?? []).some((s) => !s.current) && (
          <Btn size="sm" variant="quiet" onClick={() => void signOutOthers()}>
            Sign out everywhere else
          </Btn>
        )}
      </div>
      <Loaded query={sessions} sk={<SkItems rows={2} />} what="where you’re signed in">
        {(list) =>
          list.length === 0 ? (
            <Empty title="Nowhere else." />
          ) : (
            <div className="items flush" style={{ marginTop: 6 }}>
              {list
                .slice()
                .sort((a, b) => Number(b.current) - Number(a.current))
                .map((s) => (
                  <div key={s.id} className="item">
                    <div className="grow" style={{ minWidth: 0 }}>
                      <div className="title">
                        {deviceLabel(s.user_agent)}
                        {s.current && (
                          <span className="t3" style={{ fontWeight: 400 }}>
                            {' '}
                            (this one)
                          </span>
                        )}
                      </div>
                      <div className="meta">
                        <span className="mono">{s.ip}</span>, last active {relTime(toMs(s.last_used_at) ?? toMs(s.created_at))}
                      </div>
                    </div>
                    {!s.current && (
                      <Btn size="sm" onClick={() => signOut(s)}>
                        Sign out
                      </Btn>
                    )}
                  </div>
                ))}
            </div>
          )
        }
      </Loaded>
    </div>
  )
}
