import { useEffect, useMemo, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Users,
  Crown,
  Shield,
  Ban,
  Loader2,
  Search,
  UserPlus,
  ChevronDown,
  ChevronRight,
  Gamepad2,
} from 'lucide-react'
import { clsx } from 'clsx'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Dialog, ConfirmDialog } from '@/components/ui/dialog'
import { api } from '@/lib/api'
import { useNotifications } from '@/store/notifications'
import type { Player, PlayerActionKind, ServerStatus } from '@/lib/types'
import { PlayerDetailDialog } from './detail'
import { PlayerActionsMenu } from './actions-menu'
import { BansView } from './bans'

interface PlayersPanelProps {
  serverId: string
  status: ServerStatus
}

const NAME_RE = /^[A-Za-z0-9_]{1,16}$/
const OFFLINE_PAGE = 50

type FilterKey = 'all' | 'online' | 'operators' | 'whitelisted' | 'banned' | 'bedrock'
type SortKey = 'recent' | 'name' | 'status'
type PanelView = 'roster' | 'bans'

// Segmented toggle between the live roster and the ban-management view. Shared
// by both views so it stays put when switching.
function ViewToggle({ view, onChange }: { view: PanelView; onChange: (v: PanelView) => void }) {
  const tabs: { key: PanelView; label: string }[] = [
    { key: 'roster', label: 'Roster' },
    { key: 'bans', label: 'Bans' },
  ]
  return (
    <div className="flex-shrink-0 flex gap-1 px-4 pt-3 bg-surface" role="tablist">
      {tabs.map((t) => (
        <button
          key={t.key}
          type="button"
          role="tab"
          aria-selected={view === t.key}
          onClick={() => onChange(t.key)}
          className={clsx(
            'rounded-md px-3 py-1 text-sm font-medium transition-colors',
            view === t.key
              ? 'bg-surface-2 text-text-primary'
              : 'text-text-secondary hover:text-text-primary',
          )}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}

// A name is acceptable if it is a plain Java name, or (when the server runs
// Floodgate) its Bedrock username prefix followed by a Java-shaped core. The
// agent re-validates, so this just guides the input.
function isValidName(name: string, bedrockPrefix?: string): boolean {
  if (NAME_RE.test(name)) return true
  if (bedrockPrefix && name.startsWith(bedrockPrefix)) {
    return NAME_RE.test(name.slice(bedrockPrefix.length))
  }
  return false
}

function formatDuration(joinedAt: string): string {
  const ms = Date.now() - new Date(joinedAt).getTime()
  const min = Math.floor(ms / 60000)
  if (min < 1) return 'just now'
  if (min < 60) return `${min}m`
  const hrs = Math.floor(min / 60)
  if (hrs < 24) return `${hrs}h ${min % 60}m`
  return `${Math.floor(hrs / 24)}d ${hrs % 24}h`
}

function formatLastSeen(lastSeen: string): string {
  const t = new Date(lastSeen).getTime()
  // Guard against missing / unparseable / epoch-zero timestamps that otherwise
  // render as absurd durations (e.g. "739781d ago").
  if (!lastSeen || Number.isNaN(t) || t <= 0) return 'never'
  const ms = Date.now() - t
  if (ms < 0) return 'just now'
  const min = Math.floor(ms / 60000)
  if (min < 1) return 'just now'
  if (min < 60) return `${min}m ago`
  const hrs = Math.floor(min / 60)
  if (hrs < 24) return `${hrs}h ago`
  const days = Math.floor(hrs / 24)
  return `${days}d ago`
}

function avatarUrl(name: string) {
  return `https://mc-heads.net/avatar/${encodeURIComponent(name)}/40`
}

// Bedrock players have no Java skin to render (mc-heads can't resolve their
// Floodgate name), so they get a distinct controller-tile avatar instead.
function PlayerAvatar({ player, className }: { player: Player; className: string }) {
  if (player.bedrock) {
    return (
      <div
        className={`${className} flex items-center justify-center rounded bg-gradient-to-br from-cyan-700/50 to-teal-800/50 text-cyan-200 ${
          player.online ? '' : 'opacity-60'
        }`}
        title="Bedrock Edition player"
      >
        <Gamepad2 className="h-4 w-4" />
      </div>
    )
  }
  return (
    <img
      src={avatarUrl(player.name)}
      alt=""
      className={`${className} rounded bg-surface-2 ${player.online ? '' : 'opacity-50 grayscale'}`}
      onError={(e) => {
        ;(e.target as HTMLImageElement).style.visibility = 'hidden'
      }}
    />
  )
}

const ACTION_VERB: Record<PlayerActionKind, string> = {
  op: 'Granted operator',
  deop: 'Removed operator',
  ban: 'Banned',
  pardon: 'Pardoned',
  ban_ip: 'IP banned',
  pardon_ip: 'IP pardoned',
  kick: 'Kicked',
  whitelist_add: 'Whitelisted',
  whitelist_remove: 'Removed from whitelist',
}

// Actions that should require an explicit confirm (and accept a reason).
const DESTRUCTIVE: ReadonlySet<PlayerActionKind> = new Set(['ban', 'kick'])

function statusRank(p: Player): number {
  if (p.online) return 0
  if (p.banned) return 1
  if (p.op) return 2
  if (p.whitelisted) return 3
  return 4
}

function StateBadges({ player }: { player: Player }) {
  return (
    <div className="flex items-center gap-1 flex-shrink-0">
      {player.bedrock && (
        <Badge
          className="bg-cyan-900/40 text-cyan-300 border border-cyan-800/50"
          title="Bedrock Edition (via Geyser)"
        >
          <Gamepad2 className="h-3 w-3" /> BE
        </Badge>
      )}
      {player.op && (
        <Badge variant="warning" title={`Operator (level ${player.op_level ?? 4})`}>
          OP
        </Badge>
      )}
      {player.whitelisted && (
        <Badge variant="success" title="Whitelisted">
          WL
        </Badge>
      )}
      {player.banned && (
        <Badge variant="error" title={player.ban_reason || 'Banned'}>
          BANNED
        </Badge>
      )}
    </div>
  )
}

function PlayerRow({
  player,
  serverOnline,
  busy,
  onAction,
  onOpen,
  onCopyUuid,
}: {
  player: Player
  serverOnline: boolean
  busy: boolean
  onAction: (kind: PlayerActionKind, player: Player) => void
  onOpen: (player: Player) => void
  onCopyUuid: (player: Player) => void
}) {
  return (
    <div className="flex items-center gap-3 px-4 py-2.5 border-b border-border/50 hover:bg-surface-2/30">
      <button
        type="button"
        onClick={() => onOpen(player)}
        className="flex items-center gap-3 flex-1 min-w-0 text-left rounded -mx-1 px-1 py-0.5"
        title="View saved data"
      >
        <div className="relative flex-shrink-0">
          <PlayerAvatar player={player} className="h-9 w-9" />
          <span
            className={`absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full border-2 border-surface ${
              player.online ? 'bg-green-500' : 'bg-text-secondary/40'
            }`}
            title={player.online ? 'Online' : 'Offline'}
          />
        </div>
        <div className="flex-1 min-w-0">
          <p className="text-sm font-medium text-text-primary truncate">{player.name}</p>
          <p className="text-xs text-text-secondary mt-0.5">
            {player.online
              ? `online ${player.joined_at ? formatDuration(player.joined_at) : ''}`
              : `last seen ${player.last_seen ? formatLastSeen(player.last_seen) : 'unknown'}`}
          </p>
        </div>
      </button>
      <StateBadges player={player} />
      <PlayerActionsMenu
        player={player}
        serverOnline={serverOnline}
        busy={busy}
        onAction={(kind) => onAction(kind, player)}
        onOpen={() => onOpen(player)}
        onCopyUuid={player.uuid ? () => onCopyUuid(player) : undefined}
      />
    </div>
  )
}

function FilterChip({
  active,
  onClick,
  children,
  count,
}: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
  count: number
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium transition-colors ${
        active
          ? 'bg-accent text-black'
          : 'bg-surface-2 text-text-secondary hover:text-text-primary'
      }`}
    >
      {children}
      <span className={active ? 'text-black/70' : 'text-text-secondary/70'}>{count}</span>
    </button>
  )
}

// The three intents the Add-player dialog supports, each a distinct tab.
type AddTab = 'whitelist_add' | 'op' | 'ban'

const ADD_TABS: { key: AddTab; label: string; icon: React.ReactNode }[] = [
  { key: 'whitelist_add', label: 'Whitelist', icon: <Shield className="h-3.5 w-3.5" /> },
  { key: 'op', label: 'Operator', icon: <Crown className="h-3.5 w-3.5" /> },
  { key: 'ban', label: 'Ban', icon: <Ban className="h-3.5 w-3.5" /> },
]

// Minecraft operator permission levels (server.properties op-permission-level),
// summarised for the level picker.
const OP_LEVELS: { level: number; desc: string }[] = [
  { level: 1, desc: 'Bypass spawn protection.' },
  { level: 2, desc: 'Cheats: /gamemode, /give, /tp, edit command blocks.' },
  { level: 3, desc: 'Player management: /kick, /ban, /op.' },
  { level: 4, desc: 'Full control: /stop, /save-all, server config.' },
]

const ADD_SUBMIT: Record<AddTab, { label: string; verb: string }> = {
  whitelist_add: { label: 'Add to whitelist', verb: 'whitelisted' },
  op: { label: 'Make operator', verb: 'opped' },
  ban: { label: 'Ban player', verb: 'banned' },
}

// A focused, tabbed capture for adding a player by name to the whitelist, the
// operator list, or the ban list — each tab shows only the fields that intent
// needs (a level picker for op, a reason for ban). Works online (live command)
// or offline (edits the JSON files directly).
function AddPlayerDialog({
  open,
  onClose,
  onSubmit,
  busy,
  bedrockPrefix,
  serverOnline,
}: {
  open: boolean
  onClose: () => void
  onSubmit: (
    kind: PlayerActionKind,
    name: string,
    opts: { reason?: string; level?: number },
  ) => void
  busy: boolean
  bedrockPrefix?: string
  serverOnline: boolean
}) {
  const [tab, setTab] = useState<AddTab>('whitelist_add')
  const [name, setName] = useState('')
  const [reason, setReason] = useState('')
  const [level, setLevel] = useState(4)
  const trimmed = name.trim()
  const valid = isValidName(trimmed, bedrockPrefix)

  useEffect(() => {
    if (open) {
      setTab('whitelist_add')
      setName('')
      setReason('')
      setLevel(4)
    }
  }, [open])

  const submit = () => {
    if (!valid) return
    if (tab === 'ban') onSubmit('ban', trimmed, { reason: reason.trim() || undefined })
    else if (tab === 'op')
      // A specific level only sticks via the offline ops.json edit; a live
      // server's /op always grants its default level, so don't send one.
      onSubmit('op', trimmed, serverOnline ? {} : { level })
    else onSubmit('whitelist_add', trimmed, {})
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Add player"
      description="Add by name — applied live when the server is online, or written to the JSON files when it's offline."
    >
      {/* Intent tabs */}
      <div role="tablist" className="mb-4 flex gap-1 rounded-lg bg-surface-2 p-1">
        {ADD_TABS.map((t) => (
          <button
            key={t.key}
            type="button"
            role="tab"
            aria-selected={tab === t.key}
            onClick={() => setTab(t.key)}
            className={clsx(
              'flex flex-1 items-center justify-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors',
              tab === t.key
                ? 'bg-surface text-text-primary shadow-sm'
                : 'text-text-secondary hover:text-text-primary',
            )}
          >
            {t.icon} {t.label}
          </button>
        ))}
      </div>

      <div className="space-y-4">
        <div>
          <label className="mb-1 block text-xs font-medium text-text-secondary">
            Player name
          </label>
          <Input
            placeholder="e.g. Notch"
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') submit()
            }}
            autoFocus
          />
          {trimmed && !valid && (
            <p className="mt-1 text-xs text-red-400">
              Names are 1–16 characters: letters, digits, underscore.
              {bedrockPrefix ? ` Bedrock players use the "${bedrockPrefix}" prefix.` : ''}
            </p>
          )}
        </div>

        {tab === 'op' &&
          (serverOnline ? (
            <p className="rounded-md border border-border bg-surface-2/50 px-3 py-2 text-xs text-text-secondary">
              The server is online, so the player is opped at its default permission
              level. Stop the server to assign a specific level.
            </p>
          ) : (
            <div>
              <span className="mb-1.5 block text-xs font-medium text-text-secondary">
                Permission level
              </span>
              <div className="grid grid-cols-4 gap-1.5">
                {OP_LEVELS.map((l) => (
                  <button
                    key={l.level}
                    type="button"
                    onClick={() => setLevel(l.level)}
                    className={clsx(
                      'rounded-md border py-1.5 text-sm font-medium transition-colors',
                      level === l.level
                        ? 'border-accent bg-accent/15 text-text-primary'
                        : 'border-border text-text-secondary hover:text-text-primary',
                    )}
                  >
                    {l.level}
                  </button>
                ))}
              </div>
              <p className="mt-1.5 text-xs text-text-secondary">
                {OP_LEVELS.find((l) => l.level === level)?.desc}
              </p>
            </div>
          ))}

        {tab === 'ban' && (
          <div>
            <label className="mb-1 block text-xs font-medium text-text-secondary">
              Reason <span className="text-text-secondary/60">(optional)</span>
            </label>
            <Input
              placeholder="Shown to the player when they're refused"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') submit()
              }}
            />
          </div>
        )}
      </div>

      <div className="mt-6 flex justify-end gap-2">
        <Button variant="outline" onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button
          variant={tab === 'ban' ? 'destructive' : 'default'}
          disabled={!valid || busy}
          loading={busy}
          onClick={submit}
        >
          {ADD_TABS.find((t) => t.key === tab)?.icon}
          {ADD_SUBMIT[tab].label}
        </Button>
      </div>
    </Dialog>
  )
}

export function PlayersPanel({ serverId, status }: PlayersPanelProps) {
  const { success, error } = useNotifications()
  const qc = useQueryClient()

  const [view, setView] = useState<PanelView>('roster')
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState<FilterKey>('all')
  const [sort, setSort] = useState<SortKey>('recent')
  const [detailPlayer, setDetailPlayer] = useState<Player | null>(null)
  const [offlineOpen, setOfflineOpen] = useState(true)
  const [offlineLimit, setOfflineLimit] = useState(OFFLINE_PAGE)
  const [addOpen, setAddOpen] = useState(false)
  const [confirm, setConfirm] = useState<{ kind: PlayerActionKind; player: Player } | null>(null)
  const [confirmReason, setConfirmReason] = useState('')

  const isOnline = status === 'online'

  // Re-render every 30s so relative timestamps don't freeze when the server is
  // offline and nothing else triggers a refetch.
  const [, setTick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 30_000)
    return () => clearInterval(t)
  }, [])

  const { data: players = [], isLoading } = useQuery({
    queryKey: ['players', serverId],
    queryFn: () => api.players.list(serverId),
    // Poll while online (live roster changes); fetch once when offline (the
    // playerdata files barely move).
    refetchInterval: isOnline ? 5_000 : false,
  })

  // Geyser/Floodgate install + Bedrock prefix. Rarely changes, so fetch lazily.
  const { data: meta } = useQuery({
    queryKey: ['players-meta', serverId],
    queryFn: () => api.players.meta(serverId),
    staleTime: 5 * 60_000,
  })
  const bedrockPrefix = meta?.installed ? meta.prefix || undefined : undefined

  const counts = useMemo(
    () => ({
      all: players.length,
      online: players.filter((p) => p.online).length,
      operators: players.filter((p) => p.op).length,
      whitelisted: players.filter((p) => p.whitelisted).length,
      banned: players.filter((p) => p.banned).length,
      bedrock: players.filter((p) => p.bedrock).length,
    }),
    [players],
  )

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return players.filter((p) => {
      if (q && !p.name.toLowerCase().includes(q)) return false
      switch (filter) {
        case 'online':
          return p.online
        case 'operators':
          return !!p.op
        case 'whitelisted':
          return !!p.whitelisted
        case 'banned':
          return !!p.banned
        case 'bedrock':
          return !!p.bedrock
        default:
          return true
      }
    })
  }, [players, search, filter])

  const comparator = useMemo(() => {
    return (a: Player, b: Player) => {
      if (sort === 'name') return a.name.localeCompare(b.name)
      if (sort === 'status') {
        const r = statusRank(a) - statusRank(b)
        return r !== 0 ? r : a.name.localeCompare(b.name)
      }
      // recent: online (by longest session) first, then most-recently-seen.
      if (a.online && b.online) {
        const ja = a.joined_at ? new Date(a.joined_at).getTime() : 0
        const jb = b.joined_at ? new Date(b.joined_at).getTime() : 0
        return ja - jb
      }
      const ta = a.last_seen ? new Date(a.last_seen).getTime() : 0
      const tb = b.last_seen ? new Date(b.last_seen).getTime() : 0
      return tb - ta
    }
  }, [sort])

  const onlinePlayers = useMemo(
    () => filtered.filter((p) => p.online).sort(comparator),
    [filtered, comparator],
  )
  const offlinePlayers = useMemo(
    () => filtered.filter((p) => !p.online).sort(comparator),
    [filtered, comparator],
  )

  const action = useMutation({
    mutationFn: (vars: {
      action: PlayerActionKind
      name: string
      uuid?: string
      reason?: string
      level?: number
    }) => api.players.action(serverId, vars),
    onSuccess: (_d, vars) => {
      qc.invalidateQueries({ queryKey: ['players', serverId] })
      if (vars.uuid) {
        qc.invalidateQueries({ queryKey: ['player-detail', serverId, vars.uuid] })
      }
      success(
        `${ACTION_VERB[vars.action]} ${vars.name}`,
        isOnline ? 'Command sent to the live server' : 'Edited the server config files',
      )
    },
    onError: (e: Error) => error('Action failed', e.message),
  })

  const applyAction = (
    kind: PlayerActionKind,
    player: Player,
    opts: { reason?: string; level?: number } = {},
  ) => {
    action.mutate({
      action: kind,
      name: player.name,
      uuid: player.uuid,
      reason: opts.reason,
      level: opts.level,
    })
  }

  const requestAction = (kind: PlayerActionKind, player: Player) => {
    if (DESTRUCTIVE.has(kind)) {
      setConfirmReason('')
      setConfirm({ kind, player })
    } else {
      applyAction(kind, player)
    }
  }

  const copyUuid = async (player: Player) => {
    if (!player.uuid) return
    try {
      await navigator.clipboard.writeText(player.uuid)
      success('UUID copied', player.uuid)
    } catch {
      error('Copy failed', 'Clipboard is unavailable')
    }
  }

  // Clamp the offline page back to one page whenever the visible set shrinks.
  useEffect(() => {
    setOfflineLimit(OFFLINE_PAGE)
  }, [search, filter, sort])

  const pagedOffline = offlinePlayers.slice(0, offlineLimit)

  const FILTERS: { key: FilterKey; label: string }[] = [
    { key: 'all', label: 'All' },
    { key: 'online', label: 'Online' },
    { key: 'operators', label: 'Operators' },
    { key: 'whitelisted', label: 'Whitelisted' },
    { key: 'banned', label: 'Banned' },
    // Only surface the Bedrock filter where it's relevant (Geyser installed or
    // Bedrock players already present) to avoid clutter on Java-only servers.
    ...(meta?.installed || counts.bedrock > 0
      ? [{ key: 'bedrock' as FilterKey, label: 'Bedrock' }]
      : []),
  ]

  if (view === 'bans') {
    return (
      <div className="flex flex-col h-full">
        <ViewToggle view={view} onChange={setView} />
        <BansView serverId={serverId} status={status} />
      </div>
    )
  }

  return (
    <div className="flex flex-col h-full">
      <ViewToggle view={view} onChange={setView} />
      {/* Header */}
      <div className="flex-shrink-0 flex items-center justify-between gap-3 px-4 py-3 border-b border-border bg-surface">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h3 className="text-sm font-medium text-text-primary">
              {counts.online} online
              <span className="text-text-secondary font-normal"> · {counts.all} known</span>
            </h3>
            {meta?.installed && (
              <Badge
                className="bg-cyan-900/40 text-cyan-300 border border-cyan-800/50"
                title={`Geyser bridge detected${
                  meta.floodgate ? ' + Floodgate' : ''
                } · Bedrock players supported${
                  meta.prefix ? ` (prefix "${meta.prefix}")` : ''
                }`}
              >
                <Gamepad2 className="h-3 w-3" /> Geyser
              </Badge>
            )}
          </div>
          <p className="text-xs text-text-secondary truncate">
            {isOnline
              ? 'Live roster refreshes every 5s · saved data may lag until the next world save'
              : `Server is ${status} · roster from world files`}
          </p>
        </div>
        <Button
          size="sm"
          variant="outline"
          className="flex-shrink-0"
          onClick={() => setAddOpen(true)}
        >
          <UserPlus className="h-3.5 w-3.5" /> Add player
        </Button>
      </div>

      {/* Toolbar */}
      <div className="flex-shrink-0 space-y-2 px-4 py-2.5 border-b border-border bg-surface">
        <div className="flex items-center gap-2">
          <div className="relative flex-1 min-w-0">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-text-secondary" />
            <Input
              placeholder="Search players…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="pl-8"
            />
          </div>
          <select
            value={sort}
            onChange={(e) => setSort(e.target.value as SortKey)}
            className="h-9 rounded-md border border-border bg-surface-2 px-2 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
            title="Sort"
          >
            <option value="recent">Recent</option>
            <option value="name">Name</option>
            <option value="status">Status</option>
          </select>
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          {FILTERS.map((f) => (
            <FilterChip
              key={f.key}
              active={filter === f.key}
              onClick={() => setFilter(f.key)}
              count={counts[f.key]}
            >
              {f.label}
            </FilterChip>
          ))}
        </div>
      </div>

      {/* List */}
      <div className="flex-1 min-h-0 overflow-y-auto">
        {isLoading ? (
          <div className="flex justify-center py-8">
            <Loader2 className="h-5 w-5 animate-spin text-accent" />
          </div>
        ) : players.length === 0 ? (
          <div className="text-center py-12 text-text-secondary">
            <Users className="h-8 w-8 mx-auto mb-2 opacity-30" />
            <p className="text-sm">No players found</p>
            <p className="text-xs mt-1">No one has joined this server yet.</p>
          </div>
        ) : onlinePlayers.length === 0 && offlinePlayers.length === 0 ? (
          <div className="text-center py-12 text-text-secondary">
            <Search className="h-7 w-7 mx-auto mb-2 opacity-30" />
            <p className="text-sm">No matches</p>
            <p className="text-xs mt-1">Try a different search or filter.</p>
          </div>
        ) : (
          <>
            {onlinePlayers.length > 0 && (
              <>
                <div className="px-4 py-1.5 text-[11px] font-medium uppercase tracking-wide text-text-secondary bg-surface-2/40 border-b border-border/50 sticky top-0 z-10">
                  Online — {onlinePlayers.length}
                </div>
                {onlinePlayers.map((p) => (
                  <PlayerRow
                    key={p.uuid || p.name}
                    player={p}
                    serverOnline={isOnline}
                    busy={action.isPending}
                    onAction={requestAction}
                    onOpen={setDetailPlayer}
                    onCopyUuid={copyUuid}
                  />
                ))}
              </>
            )}
            {offlinePlayers.length > 0 && (
              <>
                <button
                  type="button"
                  onClick={() => setOfflineOpen((v) => !v)}
                  className="flex w-full items-center gap-1.5 px-4 py-1.5 text-[11px] font-medium uppercase tracking-wide text-text-secondary bg-surface-2/40 border-b border-border/50 sticky top-0 z-10 hover:text-text-primary"
                >
                  {offlineOpen ? (
                    <ChevronDown className="h-3 w-3" />
                  ) : (
                    <ChevronRight className="h-3 w-3" />
                  )}
                  Offline — {offlinePlayers.length}
                </button>
                {offlineOpen && (
                  <>
                    {pagedOffline.map((p) => (
                      <PlayerRow
                        key={p.uuid || p.name}
                        player={p}
                        serverOnline={isOnline}
                        busy={action.isPending}
                        onAction={requestAction}
                        onOpen={setDetailPlayer}
                        onCopyUuid={copyUuid}
                      />
                    ))}
                    {offlinePlayers.length > pagedOffline.length && (
                      <div className="flex items-center justify-center gap-3 px-4 py-3 text-xs text-text-secondary">
                        <span>
                          Showing {pagedOffline.length} of {offlinePlayers.length}
                        </span>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => setOfflineLimit((n) => n + OFFLINE_PAGE)}
                        >
                          Load more
                        </Button>
                      </div>
                    )}
                  </>
                )}
              </>
            )}
          </>
        )}
      </div>

      {/* Add player (whitelist / operator / ban) */}
      <AddPlayerDialog
        open={addOpen}
        onClose={() => setAddOpen(false)}
        busy={action.isPending}
        bedrockPrefix={bedrockPrefix}
        serverOnline={isOnline}
        onSubmit={(kind, name, opts) => {
          applyAction(kind, { name, online: false }, opts)
          setAddOpen(false)
        }}
      />

      {/* Destructive confirm (ban / kick) */}
      <ConfirmDialog
        open={confirm !== null}
        onClose={() => setConfirm(null)}
        onConfirm={() => {
          if (confirm)
            applyAction(confirm.kind, confirm.player, {
              reason: confirmReason.trim() || undefined,
            })
          setConfirm(null)
        }}
        title={
          confirm
            ? `${confirm.kind === 'ban' ? 'Ban' : 'Kick'} ${confirm.player.name}?`
            : ''
        }
        description={
          confirm?.kind === 'ban'
            ? isOnline
              ? 'Bans the player on the live server and disconnects them if connected.'
              : 'Adds the player to banned-players.json. They will be blocked on next start.'
            : 'Disconnects the player from the running server.'
        }
        confirmLabel={confirm?.kind === 'ban' ? 'Ban' : 'Kick'}
        variant="destructive"
        loading={action.isPending}
      >
        <Input
          placeholder="Reason (optional)"
          value={confirmReason}
          onChange={(e) => setConfirmReason(e.target.value)}
          autoFocus
        />
      </ConfirmDialog>

      <PlayerDetailDialog
        serverId={serverId}
        player={detailPlayer}
        serverOnline={isOnline}
        open={detailPlayer !== null}
        onClose={() => setDetailPlayer(null)}
      />
    </div>
  )
}
