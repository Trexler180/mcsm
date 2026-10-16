import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { PERMISSION_MODEL, collapsePerms, samePerms, type PermissionGroupDef } from '@/lib/permissions'
import type { Server, ServerMember, ServerPermission } from '@/lib/types'
import { useAuthStore } from '@/store/auth'
import { Btn, Check, Field, Filters, Notice } from '@/ui/controls'
import { act } from '@/ui/failure'
import { Dialog, MenuButton, MenuItem } from '@/ui/overlays'
import { Avatar } from '@/ui/pixel'
import { Loaded, SkTable } from '@/ui/skeleton'
import { ask } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { cap } from '@/ui/util'

// Who besides administrators may manage a server, and what each may do.

type Perm = ServerPermission

const PRESETS: { label: string; perms: Perm[] }[] = [
  { label: 'Can only look', perms: ['view'] },
  { label: 'Moderator', perms: ['view', 'console', 'players.whitelist', 'players.kick', 'players.ban', 'players.inspect'] },
  { label: 'Runs the server', perms: ['view', 'power', 'console', 'players', 'mods', 'backups', 'tasks', 'files.read'] },
  { label: 'Everything', perms: PERMISSION_MODEL.map((g) => g.group) },
]

/** "Power, console and some players". */
export function permSummary(ps: Perm[]): string {
  const held = new Set(collapsePerms(ps))
  // Every action of a group is the group.
  const whole = (g: PermissionGroupDef) => held.has(g.group) || (g.leaves.length > 0 && g.leaves.every((l) => held.has(l.value)))
  if (PERMISSION_MODEL.every(whole)) return 'Everything'
  const full = PERMISSION_MODEL.filter(whole).map((g) => g.label.toLowerCase())
  const part = PERMISSION_MODEL.filter((g) => !whole(g) && g.leaves.some((l) => held.has(l.value))).map((g) => 'some ' + g.label.toLowerCase())
  const all = [...full, ...part]
  return cap(all.length > 1 ? all.slice(0, -1).join(', ') + ' and ' + all[all.length - 1] : all[0] || 'nothing')
}

export function AccessSettings({ server }: { server: Server }) {
  const members = useQuery({ queryKey: ['server-members', server.id], queryFn: () => api.servers.members(server.id) })
  return (
    <Loaded query={members} sk={<SkTable rows={4} cols={3} />} what={`who can manage ${server.name}`}>
      {(m) => <AccessBody server={server} owner={m.owner} members={m.members} />}
    </Loaded>
  )
}

function AccessBody({ server, owner, members }: { server: Server; owner: ServerMember; members: ServerMember[] }) {
  const qc = useQueryClient()
  const [edit, setEdit] = useState<ServerMember | 'new' | null>(null)
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['server-members', server.id] })
    void qc.invalidateQueries({ queryKey: ['server-permissions', server.id] })
  }
  const remove = async (m: ServerMember) => {
    const name = m.display_name || m.email
    if (!(await ask({ title: `Remove ${name} from ${server.name}?`, body: 'They lose access straight away.', confirm: 'Remove', danger: true }))) return
    const r = await act(`remove ${name}`, () => api.servers.removeMember(server.id, m.user_id).then(() => true))
    if (r === undefined) return
    toast('ok', `Removed ${name}`)
    refresh()
  }
  return (
    <div>
      <div className="toolbar" style={{ alignItems: 'flex-start' }}>
        <div className="grow">
          <p className="t2" style={{ maxWidth: '60ch' }}>
            Administrators can always manage every server. Anyone else needs to be added here, with just what they need.
          </p>
        </div>
        <Btn onClick={() => setEdit('new')}>Add someone</Btn>
      </div>
      {server.isolated === false && (
        <div style={{ marginBottom: 14 }}>
          <Notice tone="warn">
            <b>This server’s node doesn’t keep it apart from the panel.</b> Anyone who can edit its files or install its mods can run code as the panel itself, so give Files: edit and Mods: install only to people you’d trust as an administrator.
          </Notice>
        </div>
      )}
      <table className="t">
        <tbody>
          <tr>
            <td style={{ width: 280 }}>
              <span className="row" style={{ gap: 10 }}>
                <Avatar name={owner.display_name || owner.email} size={22} />
                <span className="strong ellipsis">{owner.display_name || owner.email}</span>
                <span className="t3 small">owner</span>
              </span>
            </td>
            <td className="t2">Everything</td>
            <td />
          </tr>
          {members.map((m) => (
            <tr key={m.user_id}>
              <td>
                <span className="row" style={{ gap: 10 }}>
                  <Avatar name={m.display_name || m.email} size={22} />
                  <span className="strong ellipsis">{m.display_name || m.email}</span>
                  {m.role === 'admin' && <span className="t3 small">administrator</span>}
                </span>
              </td>
              <td className="t2">{permSummary(m.permissions)}</td>
              <td className="actions">
                <span className="row" style={{ gap: 4 }}>
                  <Btn size="sm" onClick={() => setEdit(m)}>
                    Change
                  </Btn>
                  <MenuButton
                    label="More"
                    items={(close) => (
                      <MenuItem danger close={close} onClick={() => void remove(m)}>
                        Remove
                      </MenuItem>
                    )}
                  />
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {members.length === 0 && (
        <p className="t3 small" style={{ marginTop: 12 }}>
          Just the owner so far.
        </p>
      )}
      {edit && <MemberDialog server={server} member={edit === 'new' ? null : edit} existing={[owner, ...members]} onClose={() => setEdit(null)} onSaved={refresh} />}
    </div>
  )
}

function MemberDialog({ server, member, existing, onClose, onSaved }: { server: Server; member: ServerMember | null; existing: ServerMember[]; onClose: () => void; onSaved: () => void }) {
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  const users = useQuery({ queryKey: ['users'], queryFn: () => api.users.list(), enabled: admin && !member, staleTime: 60_000, retry: false }).data
  const [perms, setPerms] = useState<Perm[]>(member?.permissions ?? ['view'])
  const [who, setWho] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    setPerms(member?.permissions ?? ['view'])
    setWho('')
  }, [member])
  const taken = new Set(existing.map((m) => m.user_id))
  const candidates = (users ?? []).filter((u) => u.role !== 'admin' && !taken.has(u.id))
  const preset = PRESETS.find((p) => samePerms(p.perms, perms))?.label ?? ''
  const name = member ? member.display_name || member.email : ''
  const valid = collapsePerms(perms).length > 0 && (!!member || !!who.trim())
  const save = async () => {
    if (!valid) return
    setBusy(true)
    const target = who.trim()
    const r = await act(member ? `change what ${name} can do` : `add ${target}`, () =>
      member
        ? api.servers.updateMember(server.id, member.user_id, collapsePerms(perms), member.permissions)
        : api.servers.addMember(server.id, target.includes('@') ? { email: target, permissions: collapsePerms(perms) } : { user_id: target, permissions: collapsePerms(perms) }),
    )
    setBusy(false)
    if (r === undefined) return
    toast('ok', member ? `Updated what ${name} can do` : `${r.display_name || r.email} can now manage ${server.name}`)
    onSaved()
    onClose()
  }
  return (
    <Dialog
      open
      onClose={onClose}
      size="wide"
      title={member ? `What ${name} can do on ${server.name}` : `Add someone to ${server.name}`}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" disabled={!valid} loading={busy} onClick={() => void save()}>
            {member ? 'Save' : 'Add'}
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 16 }}>
        {!member && (
          <Field label="Who" hint={admin ? 'Their email, or pick someone with an account here.' : 'The email they sign in with.'}>
            <input className="input" type="email" list="access-candidates" value={who} onChange={(e) => setWho(e.target.value)} placeholder="helper@example.com" autoFocus />
            {candidates.length > 0 && (
              <datalist id="access-candidates">
                {candidates.map((u) => (
                  <option key={u.id} value={u.email}>
                    {u.display_name || u.email}
                  </option>
                ))}
              </datalist>
            )}
          </Field>
        )}
        <Field label="Start from">
          <Filters
            value={preset}
            onChange={(l) => setPerms([...(PRESETS.find((p) => p.label === l)?.perms ?? ['view'])])}
            options={PRESETS.map((p) => ({ value: p.label, label: p.label }))}
          />
        </Field>
        <PermissionEditor value={perms} onChange={setPerms} />
      </div>
    </Dialog>
  )
}

/** Each group with its single actions; a group grants everything under it. */
export function PermissionEditor({ value, onChange }: { value: Perm[]; onChange: (v: Perm[]) => void }) {
  const has = (p: Perm) => value.includes(p)
  const setGroup = (g: PermissionGroupDef, on: boolean) =>
    onChange(on ? [...value.filter((p) => !p.startsWith(g.group + '.')), g.group] : value.filter((p) => p !== g.group && !p.startsWith(g.group + '.')))
  const setLeaf = (g: PermissionGroupDef, leaf: Perm, on: boolean) => {
    const leaves = g.leaves.map((l) => l.value)
    let next = value.filter((p) => p !== g.group)
    if (has(g.group)) next = [...next, ...leaves]
    next = on ? [...next, leaf] : next.filter((p) => p !== leaf)
    next = Array.from(new Set(next))
    if (leaves.length && leaves.every((l) => next.includes(l))) next = [...next.filter((p) => !leaves.includes(p)), g.group]
    onChange(next)
  }
  return (
    <div>
      {PERMISSION_MODEL.map((g) => {
        const all = has(g.group)
        const some = g.leaves.filter((l) => has(l.value)).length
        return (
          <div key={g.group} className="setting" style={{ alignItems: 'flex-start' }}>
            <div className="text">
              <Check checked={all} indeterminate={!all && some > 0} onChange={(v) => setGroup(g, v)} label={<span style={{ fontWeight: 500 }}>{g.label}</span>} />
              <div className="desc" style={{ marginLeft: 25 }}>
                {g.detail}
              </div>
              {g.leaves.length > 0 && (
                <div className="row wrap" style={{ gap: '6px 16px', marginTop: 8, marginLeft: 25 }}>
                  {g.leaves.map((l) => (
                    <Check key={l.value} checked={all || has(l.value)} onChange={(on) => setLeaf(g, l.value, on)} label={<span className="small">{l.label}</span>} />
                  ))}
                </div>
              )}
            </div>
          </div>
        )
      })}
    </div>
  )
}
