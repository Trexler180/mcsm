import { useEffect, useRef, useState, type DragEvent, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { FleetSummary, ServerFolder } from '@/lib/types'
import { useAuthStore } from '@/store/auth'
import { useNotificationFeed } from '@/store/notification-feed'
import { Status, useSlider } from '@/ui/controls'
import { act } from '@/ui/failure'
import { BrandMark, Icon, type IconName } from '@/ui/icon'
import { Menu, MenuButton, MenuItem, MenuSep } from '@/ui/overlays'
import { Avatar } from '@/ui/pixel'
import { ask, setUi } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { cx, plural } from '@/ui/util'
import { effStatus, groupFleet, placeServer, serverAside, useFleet, useFolders, withPlacement } from './fleet'
import { useGo, useSection } from './nav'
import { paths } from './paths'
import { useTheme } from './theme'

const ROLE: Record<string, string> = { admin: 'Administrator', operator: 'Operator', user: 'Member' }

export function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const go0 = useGo()
  const { section, serverId } = useSection()
  const user = useAuthStore((s) => s.user)
  const admin = user?.role === 'admin'
  const unread = useNotificationFeed((s) => s.unread)
  const fleet = useFleet().data
  const offline = fleet?.nodes.filter((n) => !n.online).length ?? 0
  const go = (to: string) => {
    go0(to)
    onNavigate?.()
  }
  const [navRef, slider] = useSlider<HTMLElement>('[aria-current="page"]')
  const link = (to: string, label: string, icon: IconName, opts: { active?: boolean; aside?: ReactNode; asideTone?: string; disabled?: boolean; tip?: string } = {}) => (
    <button
      type="button"
      className={cx('nav-link', opts.disabled && 'disabled')}
      aria-current={opts.active ? 'page' : undefined}
      aria-disabled={opts.disabled || undefined}
      onClick={() => !opts.disabled && go(to)}
      data-tip={opts.tip}
    >
      <Icon name={icon} size={16} />
      <span className="ellipsis">{label}</span>
      {opts.aside != null && <span className={cx('aside', opts.asideTone)}>{opts.aside}</span>}
    </button>
  )
  const adminOnly = (what: string) => (admin ? undefined : `Only administrators can ${what}`)
  return (
    <aside className="side" aria-label="Main">
      <div className="side-top">
        <div className="brand">
          <BrandMark />
          MCSM
        </div>
        <button type="button" className="side-search" onClick={() => setUi({ palette: true })}>
          <Icon name="search" size={14} />
          <span className="grow">Search</span>
          <span className="kbd">Ctrl K</span>
        </button>
      </div>
      <nav className="side-nav" ref={navRef}>
        {slider}
        <div className="nav-group">
          {link(paths.home, 'Home', 'dashboard', { active: section === 'home' })}
          {link(paths.alerts, 'Alerts', 'bell', { active: section === 'alerts', aside: unread || null })}
        </div>
        <ServerNav fleet={fleet} current={section === 'server' ? serverId : undefined} admin={admin} go={go} />
        <div className="nav-group">
          {link(paths.nodes, 'Nodes', 'server', {
            active: section === 'nodes',
            aside: admin && offline ? offline + ' offline' : null,
            asideTone: offline ? 'warn' : undefined,
            disabled: !admin,
            tip: adminOnly('manage nodes'),
          })}
          {link(paths.people, 'People', 'users', { active: section === 'people', disabled: !admin, tip: adminOnly('manage people') })}
          {link(paths.audit, 'Audit log', 'scroll', { active: section === 'audit', disabled: !admin, tip: adminOnly('read the audit log') })}
          {link(paths.settings, 'Settings', 'settings', { active: section === 'settings', disabled: !admin, tip: adminOnly('change panel settings') })}
        </div>
      </nav>
      <div className="side-bottom">
        <MeMenu onNavigate={onNavigate} />
      </div>
    </aside>
  )
}

interface Drop {
  fid: string | null
  index: number
  sid?: string
  pos?: 'before' | 'after'
  head?: boolean
}

/** Servers grouped by folder. Administrators can add, rename, reorder and
 *  delete folders here, and drag servers between folders or reorder them. */
function ServerNav({ fleet, current, admin, go }: { fleet: FleetSummary | undefined; current?: string; admin: boolean; go: (to: string) => void }) {
  const qc = useQueryClient()
  const folders = useFolders().data ?? []
  const [drag, setDrag] = useState<string | null>(null)
  const [drop, setDrop] = useState<Drop | null>(null)
  const [editing, setEditing] = useState<string | null>(null)
  if (!fleet) return <div className="nav-group" />
  const groups = groupFleet(fleet.servers, folders)
  const nodes = fleet.nodes
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['fleet'] })
    void qc.invalidateQueries({ queryKey: ['servers'] })
    void qc.invalidateQueries({ queryKey: ['server-folders'] })
  }
  const finish = () => {
    setDrag(null)
    setDrop(null)
  }

  const moveServer = async (sid: string, fid: string | null, index: number) => {
    const s = fleet.servers.find((x) => x.id === sid)
    if (!s) return
    const from = s.folder_id && folders.some((f) => f.id === s.folder_id) ? s.folder_id : null
    const placement = placeServer(fleet, folders, sid, fid, index)
    const before = qc.getQueryData<FleetSummary>(['fleet'])
    qc.setQueryData<FleetSummary>(['fleet'], (f) => f && withPlacement(f, placement))
    const res = await act(`move ${s.name}`, () => api.servers.place(placement))
    if (!res) {
      if (before) qc.setQueryData(['fleet'], before)
      return
    }
    refresh()
    if (from === fid) return
    const name = (id: string | null) => folders.find((f) => f.id === id)?.name ?? 'Other'
    // Undo puts back only this server, so undoing an older move can't revert later ones.
    const was = res.previous.filter((p) => p.id === sid)
    toast('ok', fid ? `Moved ${s.name} to ${name(fid)}` : `Took ${s.name} out of ${name(from)}`, undefined, {
      actions: [
        {
          label: 'Undo',
          run: async () => {
            await act(`move ${s.name} back`, () => api.servers.place(was))
            refresh()
          },
        },
      ],
    })
  }

  const onDrop = (e: DragEvent) => {
    e.preventDefault()
    if (drag && drop) void moveServer(drag, drop.fid, drop.index)
    finish()
  }
  const overServer = (e: DragEvent<HTMLElement>, fid: string | null, i: number, sid: string) => {
    if (!drag) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'move'
    const r = e.currentTarget.getBoundingClientRect()
    const after = e.clientY > r.top + r.height / 2
    const next: Drop = { fid, index: after ? i + 1 : i, sid, pos: after ? 'after' : 'before' }
    if (!drop || drop.sid !== next.sid || drop.pos !== next.pos) setDrop(next)
  }
  const overFolder = (e: DragEvent, fid: string | null, len: number) => {
    if (!drag) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'move'
    if (!drop || drop.sid || drop.fid !== fid) setDrop({ fid, index: len, head: true })
  }

  const showHeads = folders.length > 0 || editing === 'new'
  const ordered = groups.filter((g) => g.folder).map((g) => g.folder as ServerFolder)
  return (
    <div
      className={cx('nav-group', drag && 'dragging-any')}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDrop(null)
      }}
    >
      <div className="nav-label">
        Servers
        {admin && (
          <MenuButton
            icon="plus"
            label="Add a server or folder"
            size="sm"
            align="end"
            width={200}
            items={(close) => (
              <>
                <MenuItem icon="server" close={close} onClick={() => go(paths.newServer)}>
                  Add a server
                </MenuItem>
                <MenuItem icon="folder-plus" close={close} onClick={() => setEditing('new')}>
                  New folder
                </MenuItem>
              </>
            )}
          />
        )}
      </div>
      {editing === 'new' && (
        <FolderNameInput
          initial=""
          onDone={async (name) => {
            setEditing(null)
            if (!name) return
            const sort = Math.max(-1, ...folders.map((f) => f.sort_order)) + 1
            if (await act(`create the ${name} folder`, () => api.serverFolders.create({ name, sort_order: sort }))) refresh()
          }}
        />
      )}
      {groups.map(({ folder, servers }, gi) => {
        const fid = folder?.id ?? null
        // Other only shows when it has servers, or while dragging so a server can leave its folder.
        if (!folder && !servers.length && !drag) return null
        const into = !!drop?.head && drop.fid === fid
        return (
          <div key={fid ?? 'other'} className="nav-folder-group">
            {showHeads && (
              <FolderHead
                folder={folder}
                first={gi === 0}
                last={gi === ordered.length - 1}
                admin={admin}
                into={into}
                editing={!!folder && editing === folder.id}
                onEdit={(on) => setEditing(on && folder ? folder.id : null)}
                ordered={ordered}
                onChanged={refresh}
                onDragOver={(e) => overFolder(e, fid, servers.length)}
                onDrop={onDrop}
              />
            )}
            {servers.map((s, i) => {
              const a = serverAside(s, nodes)
              const mark = drop?.sid === s.id ? 'drop-' + drop.pos : null
              return (
                <button
                  type="button"
                  key={s.id}
                  className={cx('nav-link', mark, drag === s.id && 'dragging')}
                  aria-current={current === s.id ? 'page' : undefined}
                  onClick={() => go(paths.server(s.id))}
                  draggable={admin}
                  onDragStart={(e) => {
                    e.dataTransfer.effectAllowed = 'move'
                    e.dataTransfer.setData('text/plain', s.id)
                    setDrag(s.id)
                  }}
                  onDragEnd={finish}
                  onDragOver={(e) => overServer(e, fid, i, s.id)}
                  onDrop={onDrop}
                >
                  <span className="slot">
                    <Status status={effStatus(s, nodes)} quiet />
                  </span>
                  <span className="ellipsis">{s.name}</span>
                  {a && (
                    <span className={cx('aside', a.tone)} data-tip={a.tip}>
                      {a.text}
                    </span>
                  )}
                </button>
              )
            })}
            {!servers.length && (
              <div className={cx('nav-empty', into && 'drop-into')} onDragOver={(e) => overFolder(e, fid, 0)} onDrop={onDrop}>
                {folder ? (admin ? 'Empty. Drag a server here.' : 'Empty') : 'Drop here to take it out of its folder'}
              </div>
            )}
          </div>
        )
      })}
      {fleet.servers.length === 0 && <div className="nav-empty">{admin ? 'No servers yet.' : 'No servers shared with you yet.'}</div>}
    </div>
  )
}

function FolderNameInput({ initial, onDone }: { initial: string; onDone: (name: string) => void }) {
  const ref = useRef<HTMLInputElement>(null)
  const done = useRef(false)
  useEffect(() => {
    ref.current?.focus()
    ref.current?.select()
  }, [])
  const finish = (value: string) => {
    if (done.current) return
    done.current = true
    onDone(value.trim())
  }
  return (
    <div className="nav-folder editing">
      <input
        ref={ref}
        className="folder-input"
        defaultValue={initial}
        placeholder="Folder name"
        aria-label="Folder name"
        maxLength={40}
        onKeyDown={(e) => {
          if (e.key === 'Enter') finish(e.currentTarget.value)
          if (e.key === 'Escape') {
            e.stopPropagation()
            finish(initial)
          }
        }}
        onBlur={(e) => finish(e.currentTarget.value)}
      />
    </div>
  )
}

function FolderHead({
  folder,
  first,
  last,
  admin,
  into,
  editing,
  onEdit,
  ordered,
  onChanged,
  onDragOver,
  onDrop,
}: {
  folder: ServerFolder | null
  first: boolean
  last: boolean
  admin: boolean
  into: boolean
  editing: boolean
  onEdit: (on: boolean) => void
  ordered: ServerFolder[]
  onChanged: () => void
  onDragOver: (e: DragEvent) => void
  onDrop: (e: DragEvent) => void
}) {
  if (folder && editing) {
    return (
      <FolderNameInput
        initial={folder.name}
        onDone={async (name) => {
          onEdit(false)
          if (!name || name === folder.name) return
          if (await act(`rename the ${folder.name} folder`, () => api.serverFolders.update(folder.id, { name }))) onChanged()
        }}
      />
    )
  }
  const shift = async (dir: -1 | 1) => {
    if (!folder) return
    const list = [...ordered]
    const i = list.findIndex((f) => f.id === folder.id)
    const j = i + dir
    if (i < 0 || j < 0 || j >= list.length) return
    ;[list[i], list[j]] = [list[j], list[i]]
    const changed = list.map((f, k) => ({ f, k })).filter(({ f, k }) => f.sort_order !== k)
    const ok = await act(`move the ${folder.name} folder`, () => Promise.all(changed.map(({ f, k }) => api.serverFolders.update(f.id, { sort_order: k }))))
    if (ok) onChanged()
  }
  const remove = async () => {
    if (!folder) return
    const n = folder.server_count
    if (
      n &&
      !(await ask({
        title: `Delete the ${folder.name} folder?`,
        body: `${n === 1 ? 'Its server moves' : `Its ${n} servers move`} to Other. The servers themselves aren’t changed.`,
        confirm: 'Delete folder',
        danger: true,
      }))
    )
      return
    // null, not undefined, when it worked: act() answers undefined for a failure.
    const res = await act(`delete the ${folder.name} folder`, async () => (await api.serverFolders.delete(folder.id)) ?? null)
    if (res === undefined) return
    onChanged()
    const undo = res?.undo
    toast('ok', `Deleted the ${folder.name} folder`, n ? `${plural(n, 'server')} moved to Other.` : undefined, {
      actions: undo
        ? [
            {
              label: 'Undo',
              run: async () => {
                await act(`restore the ${folder.name} folder`, () => api.serverFolders.restore(undo))
                onChanged()
              },
            },
          ]
        : undefined,
    })
  }
  return (
    <div className={cx('nav-folder', into && 'drop-into')} onDragOver={onDragOver} onDrop={onDrop} onDoubleClick={() => folder && admin && onEdit(true)}>
      <span className="grow ellipsis">{folder ? folder.name : 'Other'}</span>
      {folder && admin && (
        <MenuButton
          className="folder-more"
          label={`${folder.name} folder`}
          size="sm"
          align="start"
          width={190}
          items={(close) => (
            <>
              <MenuItem close={close} onClick={() => onEdit(true)}>
                Rename
              </MenuItem>
              <MenuItem close={close} disabled={first} onClick={() => void shift(-1)}>
                Move up
              </MenuItem>
              <MenuItem close={close} disabled={last} onClick={() => void shift(1)}>
                Move down
              </MenuItem>
              <MenuSep />
              <MenuItem danger close={close} onClick={() => void remove()}>
                Delete folder
              </MenuItem>
            </>
          )}
        />
      )}
    </div>
  )
}

function MeMenu({ onNavigate }: { onNavigate?: () => void }) {
  const user = useAuthStore((s) => s.user)
  const logout = useAuthStore((s) => s.logout)
  const theme = useTheme((s) => s.theme)
  const toggleTheme = useTheme((s) => s.toggle)
  const go = useGo()
  const ref = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(false)
  const close = () => setOpen(false)
  const name = user?.display_name || user?.email || ''
  return (
    <>
      <button ref={ref} type="button" className="me" onClick={() => setOpen((o) => !o)} aria-haspopup="menu" aria-expanded={open}>
        <Avatar name={name} size={26} />
        <span className="grow" style={{ textAlign: 'left', minWidth: 0 }}>
          <span className="ellipsis" style={{ display: 'block', fontWeight: 500, fontSize: 13.5 }}>
            {name}
          </span>
          <span className="tiny t3">{ROLE[user?.role ?? ''] ?? ''}</span>
        </span>
        <Icon name="chev-ud" size={14} className="t3" />
      </button>
      <Menu anchor={ref} open={open} onClose={close} align="start" width={220}>
        <MenuItem
          icon="user"
          close={close}
          onClick={() => {
            go(paths.account)
            onNavigate?.()
          }}
        >
          Your account
        </MenuItem>
        <MenuItem icon={theme === 'dark' ? 'sun' : 'moon'} close={close} onClick={toggleTheme}>
          {theme === 'dark' ? 'Light theme' : 'Dark theme'}
        </MenuItem>
        <MenuSep />
        <MenuItem icon="log-out" close={close} onClick={() => void logout()}>
          Sign out
        </MenuItem>
      </Menu>
    </>
  )
}
