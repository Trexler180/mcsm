import { useState, type ReactNode } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { Users, UserX, Crown, Shield, Ban, Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog } from '@/components/ui/dialog'
import { api } from '@/lib/api'
import { useNotifications } from '@/store/notifications'
import type { Player, ServerStatus } from '@/lib/types'
import { PlayerDetailDialog } from './detail'

interface PlayersPanelProps {
  serverId: string
  status: ServerStatus
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
  const ms = Date.now() - new Date(lastSeen).getTime()
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

type CommandKind = 'kick' | 'ban' | 'op' | 'deop' | 'whitelist'

function PromptDialog({
  open,
  onClose,
  onConfirm,
  title,
  description,
  placeholder,
  confirmLabel,
  loading,
}: {
  open: boolean
  onClose: () => void
  onConfirm: (value: string) => void
  title: string
  description: string
  placeholder?: string
  confirmLabel: string
  loading?: boolean
}) {
  const [value, setValue] = useState('')
  return (
    <Dialog open={open} onClose={onClose} title={title} description={description}>
      <div className="space-y-3">
        <Input
          placeholder={placeholder}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          autoFocus
        />
        <div className="flex justify-end gap-3 pt-1">
          <Button variant="outline" onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button
            onClick={() => {
              onConfirm(value)
              setValue('')
            }}
            loading={loading}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </Dialog>
  )
}

function SectionLabel({ children }: { children: ReactNode }) {
  return (
    <div className="px-4 py-1.5 text-[11px] font-medium uppercase tracking-wide text-text-secondary bg-surface-2/40 border-b border-border/50 sticky top-0">
      {children}
    </div>
  )
}

function PlayerRow({
  player,
  onAction,
  onOpen,
  busy,
  serverOnline,
}: {
  player: Player
  onAction: (kind: CommandKind, name: string) => void
  onOpen: (player: Player) => void
  busy: boolean
  serverOnline: boolean
}) {
  // Commands can only be sent to a running server, so every action is disabled
  // while the server is offline (the roster still renders, read-only).
  const actionsDisabled = busy || !serverOnline
  return (
    <div className="flex items-center gap-3 px-4 py-3 border-b border-border/50">
      <button
        type="button"
        onClick={() => onOpen(player)}
        className="flex items-center gap-3 flex-1 min-w-0 text-left rounded -mx-1 px-1 py-0.5 hover:bg-surface-2/50 transition-colors"
        title="View saved data"
      >
        <div className="relative flex-shrink-0">
          <img
            src={avatarUrl(player.name)}
            alt=""
            className={`h-9 w-9 rounded bg-surface-2 ${player.online ? '' : 'opacity-50 grayscale'}`}
            onError={(e) => {
              ;(e.target as HTMLImageElement).style.visibility = 'hidden'
            }}
          />
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
      <div className="flex items-center gap-1.5 flex-shrink-0">
        <Button
          size="sm"
          variant="ghost"
          onClick={() => onAction('op', player.name)}
          disabled={actionsDisabled}
          title="Op (grant operator)"
        >
          <Crown className="h-3.5 w-3.5 text-yellow-400" />
        </Button>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => onAction('whitelist', player.name)}
          disabled={actionsDisabled}
          title="Add to whitelist"
        >
          <Shield className="h-3.5 w-3.5 text-blue-400" />
        </Button>
        {player.online && (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => onAction('kick', player.name)}
            disabled={actionsDisabled}
            title="Kick"
          >
            <UserX className="h-3.5 w-3.5 text-orange-400" />
          </Button>
        )}
        <Button
          size="sm"
          variant="ghost"
          onClick={() => onAction('ban', player.name)}
          disabled={actionsDisabled}
          title="Ban"
        >
          <Ban className="h-3.5 w-3.5 text-red-400" />
        </Button>
      </div>
    </div>
  )
}

export function PlayersPanel({ serverId, status }: PlayersPanelProps) {
  const { success, error } = useNotifications()
  const [addOpen, setAddOpen] = useState(false)
  const [addKind, setAddKind] = useState<'op' | 'whitelist'>('op')
  const [detailPlayer, setDetailPlayer] = useState<Player | null>(null)

  const isOnline = status === 'online'

  const { data: players = [], isLoading } = useQuery({
    queryKey: ['players', serverId],
    queryFn: () => api.players.list(serverId),
    // Poll while online (live roster changes); fetch once when offline (the
    // playerdata files barely move).
    refetchInterval: isOnline ? 5_000 : false,
  })

  const onlinePlayers = players
    .filter((p) => p.online)
    .sort((a, b) => a.name.localeCompare(b.name))
  const offlinePlayers = players
    .filter((p) => !p.online)
    .sort((a, b) => {
      // Most recently seen first; unknowns sink to the bottom.
      const ta = a.last_seen ? new Date(a.last_seen).getTime() : 0
      const tb = b.last_seen ? new Date(b.last_seen).getTime() : 0
      return tb - ta
    })

  const command = useMutation({
    mutationFn: ({ command }: { command: string }) => api.servers.command(serverId, command),
    onError: (e: Error) => error('Command failed', e.message),
  })

  const runAction = (kind: CommandKind, name: string) => {
    let cmd = ''
    switch (kind) {
      case 'kick':
        cmd = `kick ${name}`
        break
      case 'ban':
        cmd = `ban ${name}`
        break
      case 'op':
        cmd = `op ${name}`
        break
      case 'deop':
        cmd = `deop ${name}`
        break
      case 'whitelist':
        cmd = `whitelist add ${name}`
        break
    }
    command.mutate(
      { command: cmd },
      {
        onSuccess: () => success(`${kind}: ${name}`, `sent /${cmd}`),
      },
    )
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-4 py-3 border-b border-border bg-surface">
        <div>
          <h3 className="text-sm font-medium text-text-primary">
            {onlinePlayers.length} online
            <span className="text-text-secondary font-normal">
              {' · '}
              {offlinePlayers.length} offline
            </span>
          </h3>
          <p className="text-xs text-text-secondary">
            {isOnline
              ? 'Online refreshes every 5s · offline from world data'
              : `Server is ${status} · roster from world data`}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant="outline"
            disabled={!isOnline}
            title={isOnline ? undefined : 'Server must be online to run commands'}
            onClick={() => {
              setAddKind('op')
              setAddOpen(true)
            }}
          >
            <Crown className="h-3.5 w-3.5" /> Op offline
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={!isOnline}
            title={isOnline ? undefined : 'Server must be online to run commands'}
            onClick={() => {
              setAddKind('whitelist')
              setAddOpen(true)
            }}
          >
            <Shield className="h-3.5 w-3.5" /> Whitelist offline
          </Button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto">
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
        ) : (
          <>
            {onlinePlayers.length > 0 && (
              <>
                <SectionLabel>Online — {onlinePlayers.length}</SectionLabel>
                {onlinePlayers.map((p) => (
                  <PlayerRow
                    key={p.uuid || p.name}
                    player={p}
                    onAction={runAction}
                    onOpen={setDetailPlayer}
                    busy={command.isPending}
                    serverOnline={isOnline}
                  />
                ))}
              </>
            )}
            {offlinePlayers.length > 0 && (
              <>
                <SectionLabel>Offline — {offlinePlayers.length}</SectionLabel>
                {offlinePlayers.map((p) => (
                  <PlayerRow
                    key={p.uuid || p.name}
                    player={p}
                    onAction={runAction}
                    onOpen={setDetailPlayer}
                    busy={command.isPending}
                    serverOnline={isOnline}
                  />
                ))}
              </>
            )}
          </>
        )}
      </div>

      <PromptDialog
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onConfirm={(name) => {
          if (name.trim()) {
            runAction(addKind, name.trim())
          }
          setAddOpen(false)
        }}
        title={addKind === 'op' ? 'Op a player' : 'Add to whitelist'}
        description={
          addKind === 'op'
            ? 'Grant operator privileges to a player by name (works even if they are offline).'
            : 'Add a player to the whitelist by name.'
        }
        placeholder="Player name"
        confirmLabel={addKind === 'op' ? 'Op' : 'Add'}
        loading={command.isPending}
      />

      <PlayerDetailDialog
        serverId={serverId}
        player={detailPlayer}
        open={detailPlayer !== null}
        onClose={() => setDetailPlayer(null)}
      />
    </div>
  )
}
