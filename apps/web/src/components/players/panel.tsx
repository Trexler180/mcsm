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
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Dialog, ConfirmDialog } from '@/components/ui/dialog'
import { api } from '@/lib/api'
import { useNotifications } from '@/store/notifications'
import type { Player, PlayerActionKind, ServerStatus } from '@/lib/types'
import { PlayerDetailDialog } from './detail'
import { PlayerActionsMenu } from './actions-menu'

interface PlayersPanelProps {
  serverId: string
  status: ServerStatus
}

const NAME_RE = /^[A-Za-z0-9_]{1,16}$/
const OFFLINE_PAGE = 50

type FilterKey = 'all' | 'online' | 'ops' | 'whitelisted' | 'banned' | 'bedrock'
type SortKey = 'recent' | 'name' | 'status'

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

// A name + optional reason capture used by the "Add by name" dialog and by the
// destructive confirm dialog.
function AddPlayerDialog({
  open,
  onClose,
  onSubmit,
  busy,
  bedrockPrefix,
}: {
  open: boolean
  onClose: () => void
  onSubmit: (kind: PlayerActionKind, name: string, reason: string) => void
  busy: boolean
  bedrockPrefix?: string
}) {
  const [name, setName] = useState('')
  const [reason, setReason] = useState('')
  const trimmed = name.trim()
  const valid = isValidName(trimmed, bedrockPrefix)

  useEffect(() => {
    if (open) {
      setName('')
      setReason('')
    }
  }, [open])

  const fire = (kind: PlayerActionKind) => {
    if (!valid) return
    onSubmit(kind, trimmed, reason.trim())
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Add player by name"
      description="Works whether the server is online (live command) or offline (edits the JSON files directly)."
    >
      <div className="space-y-3">
        <div>
          <Input
            placeholder="Player name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoFocus
          />
          {trimmed && !valid && (
            <p className="mt-1 text-xs text-red-400">
              Names are 1–16 characters: letters, digits, underscore.
              {bedrockPrefix
                ? ` Bedrock players use the "${bedrockPrefix}" prefix.`
                : ''}
            </p>
          )}
        </div>
        <Input
          placeholder="Ban reason (optional)"
          value={reason}
          onChange={(e) => setReason(e.target.value)}
        />
        <div className="flex flex-wrap justify-end gap-2 pt-1">
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="outline" disabled={!valid || busy} onClick={() => fire('op')}>
            <Crown className="h-3.5 w-3.5 text-yellow-400" /> Op
          </Button>
          <Button variant="outline" disabled={!valid || busy} onClick={() => fire('whitelist_add')}>
            <Shield className="h-3.5 w-3.5 text-blue-400" /> Whitelist
          </Button>
          <Button variant="destructive" disabled={!valid || busy} onClick={() => fire('ban')}>
            <Ban className="h-3.5 w-3.5" /> Ban
          </Button>
        </div>
      </div>
    </Dialog>
  )
}

export function PlayersPanel({ serverId, status }: PlayersPanelProps) {
  const { success, error } = useNotifications()
  const qc = useQueryClient()

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
      ops: players.filter((p) => p.op).length,
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
        case 'ops':
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

  const applyAction = (kind: PlayerActionKind, player: Player, reason?: string) => {
    action.mutate({ action: kind, name: player.name, uuid: player.uuid, reason })
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
    { key: 'ops', label: 'Ops' },
    { key: 'whitelisted', label: 'Whitelisted' },
    { key: 'banned', label: 'Banned' },
    // Only surface the Bedrock filter where it's relevant (Geyser installed or
    // Bedrock players already present) to avoid clutter on Java-only servers.
    ...(meta?.installed || counts.bedrock > 0
      ? [{ key: 'bedrock' as FilterKey, label: 'Bedrock' }]
      : []),
  ]

  return (
    <div className="flex flex-col h-full">
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
        <Button size="sm" variant="outline" onClick={() => setAddOpen(true)}>
          <UserPlus className="h-3.5 w-3.5" /> Add by name
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

      {/* Add-by-name */}
      <AddPlayerDialog
        open={addOpen}
        onClose={() => setAddOpen(false)}
        busy={action.isPending}
        bedrockPrefix={bedrockPrefix}
        onSubmit={(kind, name, reason) => {
          applyAction(kind, { name, online: false }, reason || undefined)
          setAddOpen(false)
        }}
      />

      {/* Destructive confirm (ban / kick) */}
      <ConfirmDialog
        open={confirm !== null}
        onClose={() => setConfirm(null)}
        onConfirm={() => {
          if (confirm) applyAction(confirm.kind, confirm.player, confirmReason.trim() || undefined)
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
