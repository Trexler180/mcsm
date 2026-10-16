import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { can } from '@/lib/permissions'
import type { FleetSummary, Server, ServerPermission } from '@/lib/types'
import { useAuthStore } from '@/store/auth'
import { STATE, Status, type StatusKey } from '@/ui/controls'
import { act } from '@/ui/failure'
import { Icon } from '@/ui/icon'
import { useEscape } from '@/ui/overlays'
import { setUi, useUi } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { effStatus, groupFleet, useFleet, useFolders } from './fleet'
import { useGo } from './nav'
import { paths } from './paths'
import { useTheme } from './theme'

// Search: servers, their pages, pages of the panel, and actions. Ctrl+K from
// anywhere.

export interface PaletteItem {
  group: 'Servers' | 'Pages' | 'Do'
  label: string
  run: () => void
  hint?: string
  status?: StatusKey
  /** Shown only when searched for, not in the opening list. */
  quiet?: boolean
}

/** The tabs inside a server, and what each needs. */
export const SERVER_TABS: { value: string; label: string; perm: ServerPermission }[] = [
  { value: 'console', label: 'Console', perm: 'console' },
  { value: 'players', label: 'Players', perm: 'players' },
  { value: 'mods', label: 'Mods', perm: 'mods' },
  { value: 'worlds', label: 'Worlds', perm: 'files' },
  { value: 'files', label: 'Files', perm: 'files' },
  { value: 'backups', label: 'Backups', perm: 'backups' },
  { value: 'schedules', label: 'Schedules', perm: 'tasks' },
  { value: 'stats', label: 'Stats', perm: 'view' },
  { value: 'settings', label: 'Settings', perm: 'settings' },
]

export function paletteItems(opts: {
  fleet: FleetSummary | undefined
  order: string[]
  servers: Server[] | undefined
  admin: boolean
  theme: 'dark' | 'light'
  go: (to: string) => void
  toggleTheme: () => void
}): PaletteItem[] {
  const { fleet, servers, admin, go } = opts
  const items: PaletteItem[] = []
  const add = (group: PaletteItem['group'], label: string, run: () => void, o: Partial<PaletteItem> = {}) => items.push({ group, label, run, ...o })
  const byId = new Map((fleet?.servers ?? []).map((s) => [s.id, s]))
  for (const id of opts.order) {
    const s = byId.get(id)
    if (!s) continue
    const es = effStatus(s, fleet?.nodes)
    const perms = servers?.find((x) => x.id === id)?.permissions
    const may = (p: ServerPermission) => admin || (!!perms && can({ owner: false, global_admin: false, permissions: perms }, p))
    add('Servers', s.name, () => go(paths.server(s.id)), { status: es, hint: STATE[es]?.[0] ?? '' })
    for (const t of SERVER_TABS.slice(1)) {
      if (may(t.perm)) add('Pages', `${s.name}: ${t.label}`, () => go(paths.server(s.id, t.value)), { quiet: true })
    }
    const live = s.status === 'online' || s.status === 'starting'
    if (live) {
      if (may('power.restart'))
        add('Do', `Restart ${s.name}`, () => void act(`restart ${s.name}`, () => api.servers.restart(s.id)).then((r) => r !== undefined && toast('info', `Restarting ${s.name}`)))
      if (may('power.stop'))
        add('Do', `Stop ${s.name}`, () => void act(`stop ${s.name}`, () => api.servers.stop(s.id)).then((r) => r !== undefined && toast('info', `Stopping ${s.name}`)), { quiet: true })
    } else if (es !== 'unreachable' && may('power.start')) {
      add('Do', `Start ${s.name}`, () => void act(`start ${s.name}`, () => api.servers.start(s.id)).then((r) => r !== undefined && toast('info', `Starting ${s.name}`)))
    }
    if (may('backups.create'))
      add('Do', `Back up ${s.name}`, () => void act(`back up ${s.name}`, () => api.backups.create(s.id)).then((r) => r !== undefined && toast('info', `Backing up ${s.name}`)), { quiet: true })
  }
  add('Pages', 'Home', () => go(paths.home))
  add('Pages', 'Alerts', () => go(paths.alerts))
  add('Pages', 'Your account', () => go(paths.account))
  if (admin) {
    add('Pages', 'Nodes', () => go(paths.nodes))
    add('Pages', 'People', () => go(paths.people))
    add('Pages', 'Audit log', () => go(paths.audit))
    add('Pages', 'Settings', () => go(paths.settings))
    add('Do', 'Add a server', () => go(paths.newServer))
  }
  add('Do', opts.theme === 'dark' ? 'Switch to the light theme' : 'Switch to the dark theme', opts.toggleTheme, { quiet: true })
  return items
}

/** Every word typed must appear in the label or hint. With nothing typed,
 *  the quiet items stay out of the list. */
export function filterPalette(items: PaletteItem[], q: string): PaletteItem[] {
  const toks = q.toLowerCase().split(/\s+/).filter(Boolean)
  const hits = toks.length ? items.filter((it) => toks.every((t) => (it.label + ' ' + (it.hint || '')).toLowerCase().includes(t))) : items.filter((it) => !it.quiet)
  return hits.slice(0, 50)
}

export function Palette() {
  const open = useUi((s) => s.palette)
  const [q, setQ] = useState('')
  const [sel, setSel] = useState(0)
  const listRef = useRef<HTMLDivElement>(null)
  const fleet = useFleet().data
  const folders = useFolders().data
  const servers = useQuery({ queryKey: ['servers'], queryFn: ({ signal }) => api.servers.list(signal), enabled: open, staleTime: 30_000 }).data
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  const theme = useTheme((s) => s.theme)
  const toggleTheme = useTheme((s) => s.toggle)
  const go = useGo()
  const close = () => setUi({ palette: false })
  useEscape(open, close)
  useEffect(() => {
    if (open) {
      setQ('')
      setSel(0)
    }
  }, [open])
  const results = useMemo(() => {
    if (!open) return []
    const order = groupFleet(fleet?.servers ?? [], folders ?? []).flatMap((g) => g.servers.map((s) => s.id))
    return filterPalette(paletteItems({ fleet, order, servers, admin, theme, go, toggleTheme }), q)
  }, [open, q, fleet, folders, servers, admin, theme, go, toggleTheme])
  useEffect(() => setSel(0), [q])
  useEffect(() => {
    listRef.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })
  }, [sel])
  if (!open) return null
  const run = (it: PaletteItem) => {
    close()
    setTimeout(it.run, 10)
  }
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setSel((s) => Math.min(results.length - 1, s + 1))
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSel((s) => Math.max(0, s - 1))
    }
    if (e.key === 'Enter' && results[sel]) run(results[sel])
  }
  let last: string | null = null
  return createPortal(
    <div
      className="scrim"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) close()
      }}
    >
      <div className="palette" role="dialog" aria-label="Search">
        <div className="palette-input">
          <Icon name="search" size={16} className="t3" />
          <input autoFocus value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={onKey} placeholder="Servers, pages, actions" aria-label="Search" />
        </div>
        <div className="palette-list" ref={listRef} role="listbox">
          {results.length === 0 && (
            <div className="t2 small" style={{ padding: '18px 12px' }}>
              Nothing matches “{q}”.
            </div>
          )}
          {results.map((it, i) => {
            const g = it.group !== last ? it.group : null
            last = it.group
            return (
              <Fragment key={i}>
                {g && <div className="palette-group">{g}</div>}
                <button type="button" className="palette-item" role="option" aria-selected={i === sel} onMouseMove={() => setSel(i)} onClick={() => run(it)}>
                  {it.status && <Status status={it.status} quiet />}
                  <span className="ellipsis">{it.label}</span>
                  {it.hint && <span className="hint">{it.hint}</span>}
                </button>
              </Fragment>
            )
          })}
        </div>
      </div>
    </div>,
    document.body,
  )
}
