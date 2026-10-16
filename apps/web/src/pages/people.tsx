import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { User } from '@/lib/types'
import { useAuthStore } from '@/store/auth'
import { Btn, Choices, Copy, Empty, Field, IconBtn, Select } from '@/ui/controls'
import { act } from '@/ui/failure'
import { relTime } from '@/ui/format'
import { Dialog, MenuButton, MenuItem, MenuSep } from '@/ui/overlays'
import { PageHead } from '@/ui/page'
import { Avatar } from '@/ui/pixel'
import { Loaded, SkTable } from '@/ui/skeleton'
import { ask } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { copyText, toMs } from '@/ui/util'

// Everyone who can sign in. A person is an administrator (everything) or a
// member (only what each server's Access settings give them). The panel
// sends no email, so a new account comes with a password to pass on.

type Role = 'admin' | 'user'

const ROLES: { value: Role; label: string; desc: string }[] = [
  { value: 'user', label: 'Member', desc: 'Sees only the servers they’re added to, and does what each server’s Access settings allow.' },
  { value: 'admin', label: 'Administrator', desc: 'Can do anything, including managing people, nodes and panel settings.' },
]

const nameOf = (u: Pick<User, 'display_name' | 'email'>) => u.display_name || u.email.split('@')[0]

/** A password to pass on: long, random, and easy to read out. */
export function newPassword(length = 16): string {
  // No look-alikes (0/O, 1/l/I), so it survives being read aloud or retyped.
  const chars = 'abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'
  const bytes = new Uint32Array(length)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => chars[b % chars.length]).join('')
}

export function emailError(email: string): string | null {
  const e = email.trim()
  if (!e) return 'Enter their email address.'
  return /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(e) ? null : 'That doesn’t look like an email address.'
}

export function PeoplePage() {
  const me = useAuthStore((s) => s.user?.id)
  const users = useQuery({ queryKey: ['users'], queryFn: () => api.users.list() })
  const [adding, setAdding] = useState(false)
  const [password, setPassword] = useState<User | null>(null)
  const [renaming, setRenaming] = useState<User | null>(null)
  const qc = useQueryClient()
  const refresh = () => void qc.invalidateQueries({ queryKey: ['users'] })

  const setRole = (u: User, role: Role) =>
    void act(`change ${nameOf(u)}’s role`, () => api.users.update(u.id, { role })).then((r) => {
      if (r === undefined) return
      toast('ok', `${nameOf(u)} is ${role === 'admin' ? 'an administrator' : 'a member'} now`)
      refresh()
    })
  const resetMfa = async (u: User) => {
    if (!(await ask({ title: `Turn off two-factor for ${nameOf(u)}?`, body: 'For someone who lost their authenticator. They sign in with just their password until they set it up again under Account, and their other sessions end.', confirm: 'Turn off two-factor', danger: true }))) return
    const r = await act(`turn off two-factor for ${nameOf(u)}`, () => api.users.update(u.id, { disable_mfa: true }))
    if (r !== undefined) toast('ok', `Two-factor is off for ${nameOf(u)}`)
  }
  const remove = async (u: User) => {
    if (!(await ask({ title: `Remove ${nameOf(u)}?`, body: 'They lose access to everything straight away. What they did stays in the audit log.', confirm: 'Remove', danger: true }))) return
    const r = await act(`remove ${nameOf(u)}`, () => api.users.delete(u.id).then(() => true))
    if (!r) return
    toast('ok', `Removed ${nameOf(u)}`)
    refresh()
  }

  return (
    <div className="scroll">
      <div className="page">
        <PageHead title="People" lede="Everyone who can sign in. What a member can do on a server is set in that server’s Access settings.">
          <Btn onClick={() => setAdding(true)}>Add someone</Btn>
        </PageHead>
        <Loaded query={users} sk={<SkTable rows={4} />} what="the people on this panel">
          {(list) =>
            list.length === 0 ? (
              <Empty title="Nobody yet." />
            ) : (
              <table className="t">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th style={{ width: 190 }}>Role</th>
                    <th className="hide-sm" style={{ width: 170 }}>
                      Last signed in
                    </th>
                    <th style={{ width: 48 }} />
                  </tr>
                </thead>
                <tbody>
                  {list
                    .slice()
                    .sort((a, b) => Number(b.role === 'admin') - Number(a.role === 'admin') || nameOf(a).localeCompare(nameOf(b)))
                    .map((u) => {
                      const self = u.id === me
                      const last = toMs(u.last_login)
                      return (
                        <tr key={u.id}>
                          <td>
                            <div className="row" style={{ gap: 10 }}>
                              <Avatar name={nameOf(u)} size={28} />
                              <div style={{ minWidth: 0 }}>
                                <div className="strong ellipsis">
                                  {nameOf(u)}
                                  {self && (
                                    <span className="t3" style={{ fontWeight: 400 }}>
                                      {' '}
                                      (you)
                                    </span>
                                  )}
                                </div>
                                <div className="sub ellipsis">{u.email}</div>
                              </div>
                            </div>
                          </td>
                          <td>
                            <Select
                              size="sm"
                              value={u.role === 'admin' ? 'admin' : 'user'}
                              disabled={self}
                              aria-label={`${nameOf(u)}’s role`}
                              onChange={(v) => setRole(u, v)}
                              options={ROLES.map((r) => ({ value: r.value, label: r.label }))}
                            />
                          </td>
                          <td className="hide-sm t2">{last ? relTime(last) : <span className="t3">Never</span>}</td>
                          <td className="actions">
                            <MenuButton
                              label={`More for ${nameOf(u)}`}
                              items={(close) => (
                                <>
                                  <MenuItem close={close} onClick={() => setRenaming(u)}>
                                    Change name
                                  </MenuItem>
                                  {!self && (
                                    <>
                                      <MenuItem close={close} onClick={() => setPassword(u)}>
                                        Set a new password
                                      </MenuItem>
                                      <MenuItem close={close} onClick={() => void resetMfa(u)}>
                                        Turn off two-factor
                                      </MenuItem>
                                      <MenuSep />
                                      <MenuItem danger close={close} onClick={() => void remove(u)}>
                                        Remove
                                      </MenuItem>
                                    </>
                                  )}
                                </>
                              )}
                            />
                          </td>
                        </tr>
                      )
                    })}
                </tbody>
              </table>
            )
          }
        </Loaded>
      </div>
      {adding && <AddDialog onClose={() => setAdding(false)} onDone={refresh} />}
      {password && <PasswordDialog user={password} onClose={() => setPassword(null)} />}
      {renaming && <RenameDialog user={renaming} onClose={() => setRenaming(null)} onDone={refresh} />}
    </div>
  )
}

function PasswordField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <Field label="Password" hint="Pass this on to them. They can change it under Account once they’re in." error={value.length && value.length < 10 ? 'At least 10 characters.' : null}>
      <div className="row" style={{ gap: 8 }}>
        <input className="input mono grow" value={value} onChange={(e) => onChange(e.target.value)} autoComplete="new-password" aria-label="Password" spellCheck={false} />
        <IconBtn icon="refresh" label="Make a new one" onClick={() => onChange(newPassword())} />
        <IconBtn
          icon="copy"
          label="Copy"
          onClick={() => {
            void copyText(value)
            toast('ok', 'Copied the password')
          }}
        />
      </div>
    </Field>
  )
}

function AddDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<Role>('user')
  const [password, setPassword] = useState(newPassword)
  const [touched, setTouched] = useState(false)
  const [busy, setBusy] = useState(false)
  const [made, setMade] = useState<User | null>(null)
  const err = emailError(email)
  const add = async () => {
    setTouched(true)
    if (err || password.length < 10) return
    setBusy(true)
    const r = await act(`add ${email.trim()}`, () => api.users.create(email.trim(), password, role))
    setBusy(false)
    if (r === undefined) return
    setMade(r)
    onDone()
  }
  if (made) {
    return (
      <Dialog open onClose={onClose} title={`${nameOf(made)} can sign in now`} foot={<Btn variant="primary" onClick={onClose}>Done</Btn>}>
        <div className="stack" style={{ gap: 12 }}>
          <p className="t2">Send them the panel’s address with these. The password isn’t shown again.</p>
          <dl className="facts">
            <dt>Email</dt>
            <dd>
              <Copy text={made.email} />
            </dd>
            <dt>Password</dt>
            <dd>
              <Copy text={password} mono />
            </dd>
          </dl>
          {role === 'user' && <p className="small t3">Add them to servers from each server’s Settings, under Access.</p>}
        </div>
      </Dialog>
    )
  }
  return (
    <Dialog
      open
      onClose={onClose}
      title="Add someone"
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" loading={busy} onClick={() => void add()}>
            Add
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 16 }}>
        <Field label="Email" error={touched ? err : null}>
          <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoFocus aria-label="Email" />
        </Field>
        <Field label="Role">
          <Choices value={role} onChange={setRole} options={ROLES} />
        </Field>
        <PasswordField value={password} onChange={setPassword} />
      </div>
    </Dialog>
  )
}

function PasswordDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const [password, setPassword] = useState(newPassword)
  const [busy, setBusy] = useState(false)
  const save = async () => {
    if (password.length < 10) return
    setBusy(true)
    const r = await act(`set ${nameOf(user)}’s password`, () => api.users.update(user.id, { password }))
    setBusy(false)
    if (r === undefined) return
    toast('ok', `Set a new password for ${nameOf(user)}`, 'Their other sessions have ended.')
    onClose()
  }
  return (
    <Dialog
      open
      onClose={onClose}
      title={`New password for ${nameOf(user)}`}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" loading={busy} disabled={password.length < 10} onClick={() => void save()}>
            Set password
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 14 }}>
        <p className="t2">For someone who can’t sign in. Their old password stops working and they’re signed out everywhere.</p>
        <PasswordField value={password} onChange={setPassword} />
      </div>
    </Dialog>
  )
}

function RenameDialog({ user, onClose, onDone }: { user: User; onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState(user.display_name ?? '')
  const [busy, setBusy] = useState(false)
  const save = async () => {
    setBusy(true)
    const r = await act(`rename ${nameOf(user)}`, () => api.users.update(user.id, { display_name: name.trim() || null }))
    setBusy(false)
    if (r === undefined) return
    onDone()
    onClose()
  }
  return (
    <Dialog
      open
      onClose={onClose}
      title="Change name"
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" loading={busy} onClick={() => void save()}>
            Save
          </Btn>
        </>
      }
    >
      <form
        onSubmit={(e) => {
          e.preventDefault()
          void save()
        }}
      >
        <Field label="Name" hint={`Shown instead of ${user.email}. Leave empty to use the email.`}>
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} maxLength={64} autoFocus aria-label="Name" />
        </Field>
      </form>
    </Dialog>
  )
}
