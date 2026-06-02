import { useState } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { Users, UserX, Crown, Shield, Ban, Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog } from '@/components/ui/dialog'
import { api } from '@/lib/api'
import { useNotifications } from '@/store/notifications'
import type { Player, ServerStatus } from '@/lib/types'

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

function PlayerRow({
  player,
  onAction,
  busy,
}: {
  player: Player
  onAction: (kind: CommandKind, name: string) => void
  busy: boolean
}) {
  return (
    <div className="flex items-center gap-3 px-4 py-3 border-b border-border/50">
      <img
        src={avatarUrl(player.name)}
        alt=""
        className="h-9 w-9 rounded flex-shrink-0 bg-surface-2"
        onError={(e) => {
          ;(e.target as HTMLImageElement).style.visibility = 'hidden'
        }}
      />
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-text-primary truncate">{player.name}</p>
        <p className="text-xs text-text-secondary mt-0.5">
          online {formatDuration(player.joined_at)}
        </p>
      </div>
      <div className="flex items-center gap-1.5 flex-shrink-0">
        <Button
          size="sm"
          variant="ghost"
          onClick={() => onAction('op', player.name)}
          disabled={busy}
          title="Op (grant operator)"
        >
          <Crown className="h-3.5 w-3.5 text-yellow-400" />
        </Button>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => onAction('whitelist', player.name)}
          disabled={busy}
          title="Add to whitelist"
        >
          <Shield className="h-3.5 w-3.5 text-blue-400" />
        </Button>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => onAction('kick', player.name)}
          disabled={busy}
          title="Kick"
        >
          <UserX className="h-3.5 w-3.5 text-orange-400" />
        </Button>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => onAction('ban', player.name)}
          disabled={busy}
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

  const isOnline = status === 'online'

  const { data: players = [], isLoading } = useQuery({
    queryKey: ['players', serverId],
    queryFn: () => api.players.list(serverId),
    refetchInterval: isOnline ? 5_000 : false,
    enabled: isOnline,
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

  if (!isOnline) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-text-secondary">
        <Users className="h-10 w-10 mb-3 opacity-30" />
        <p className="text-sm">Server is {status}</p>
        <p className="text-xs mt-1">Player list is available when the server is online.</p>
      </div>
    )
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-4 py-3 border-b border-border bg-surface">
        <div>
          <h3 className="text-sm font-medium text-text-primary">
            {players.length} {players.length === 1 ? 'player' : 'players'} online
          </h3>
          <p className="text-xs text-text-secondary">Refreshes every 5s</p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant="outline"
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
            <p className="text-sm">No one's online right now</p>
          </div>
        ) : (
          players.map((p) => (
            <PlayerRow
              key={p.name}
              player={p}
              onAction={runAction}
              busy={command.isPending}
            />
          ))
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
    </div>
  )
}
