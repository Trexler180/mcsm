import { useEffect, useMemo, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Ban, CircleCheck, Globe, Loader2, Plus, Search, User } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog } from '@/components/ui/dialog'
import { api } from '@/lib/api'
import { useNotifications } from '@/store/notifications'
import type { BannedIP, BannedPlayer, PlayerActionKind, ServerStatus } from '@/lib/types'

interface BansViewProps {
  serverId: string
  status: ServerStatus
}

// A permissive check so the Add dialog can flag obvious typos before the server
// rejects them; the agent re-validates with net.ParseIP, which is authoritative.
const IPV4_RE = /^(\d{1,3}\.){3}\d{1,3}$/
function looksLikeIP(value: string): boolean {
  const v = value.trim()
  if (IPV4_RE.test(v)) {
    return v.split('.').every((o) => Number(o) <= 255)
  }
  // Loose IPv6: hex groups and colons, optional :: compression.
  return /^[0-9a-fA-F:]+$/.test(v) && v.includes(':')
}

function avatarUrl(name: string) {
  return `https://mc-heads.net/avatar/${encodeURIComponent(name)}/40`
}

// banned-players.json/banned-ips.json store a created timestamp like
// "2024-01-02 15:04:05 +0000"; show just the date, falling back to the raw
// string when it doesn't parse.
function formatCreated(created?: string): string | null {
  if (!created) return null
  const date = created.split(' ')[0]
  return /^\d{4}-\d{2}-\d{2}$/.test(date) ? date : created
}

function BanMeta({ reason, created, expires }: { reason?: string; created?: string; expires?: string }) {
  const day = formatCreated(created)
  const temporary = expires && expires.toLowerCase() !== 'forever'
  return (
    <p className="text-xs text-text-secondary mt-0.5 truncate">
      {reason || 'No reason given'}
      {day && <span className="text-text-secondary/70"> · {day}</span>}
      {temporary && <span className="text-text-secondary/70"> · until {expires}</span>}
    </p>
  )
}

function AddIPBanDialog({
  open,
  onClose,
  onSubmit,
  busy,
}: {
  open: boolean
  onClose: () => void
  onSubmit: (ip: string, reason: string) => void
  busy: boolean
}) {
  const [ip, setIp] = useState('')
  const [reason, setReason] = useState('')
  const trimmed = ip.trim()
  const valid = looksLikeIP(trimmed)

  useEffect(() => {
    if (open) {
      setIp('')
      setReason('')
    }
  }, [open])

  const submit = () => {
    if (valid) onSubmit(trimmed, reason.trim())
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Ban an IP address"
      description="Blocks all connections from this address — applied live when the server is online, or written to banned-ips.json when it's offline."
    >
      <div className="space-y-4">
        <div>
          <label className="mb-1 block text-xs font-medium text-text-secondary">IP address</label>
          <Input
            placeholder="e.g. 203.0.113.7"
            value={ip}
            onChange={(e) => setIp(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') submit()
            }}
            autoFocus
          />
          {trimmed && !valid && (
            <p className="mt-1 text-xs text-red-400">Enter a valid IPv4 or IPv6 address.</p>
          )}
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium text-text-secondary">
            Reason <span className="text-text-secondary/60">(optional)</span>
          </label>
          <Input
            placeholder="Why this address is blocked"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') submit()
            }}
          />
        </div>
      </div>
      <div className="mt-6 flex justify-end gap-2">
        <Button variant="outline" onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button variant="destructive" disabled={!valid || busy} loading={busy} onClick={submit}>
          <Ban className="h-3.5 w-3.5" /> Ban IP
        </Button>
      </div>
    </Dialog>
  )
}

function PlayerBanRow({
  ban,
  busy,
  onPardon,
}: {
  ban: BannedPlayer
  busy: boolean
  onPardon: () => void
}) {
  return (
    <div className="flex items-center gap-3 px-4 py-2.5 border-b border-border/50 hover:bg-surface-2/30">
      <img
        src={avatarUrl(ban.name)}
        alt=""
        className="h-9 w-9 flex-shrink-0 rounded bg-surface-2"
        onError={(e) => {
          ;(e.target as HTMLImageElement).style.visibility = 'hidden'
        }}
      />
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-text-primary truncate">{ban.name}</p>
        <BanMeta reason={ban.reason} created={ban.created} expires={ban.expires} />
      </div>
      <Button size="sm" variant="outline" disabled={busy} onClick={onPardon}>
        <CircleCheck className="h-3.5 w-3.5" /> Pardon
      </Button>
    </div>
  )
}

function IPBanRow({ ban, busy, onPardon }: { ban: BannedIP; busy: boolean; onPardon: () => void }) {
  return (
    <div className="flex items-center gap-3 px-4 py-2.5 border-b border-border/50 hover:bg-surface-2/30">
      <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded bg-surface-2 text-text-secondary">
        <Globe className="h-4 w-4" />
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-text-primary truncate font-mono">{ban.ip}</p>
        <BanMeta reason={ban.reason} created={ban.created} expires={ban.expires} />
      </div>
      <Button size="sm" variant="outline" disabled={busy} onClick={onPardon}>
        <CircleCheck className="h-3.5 w-3.5" /> Pardon
      </Button>
    </div>
  )
}

function SectionHeader({ children }: { children: React.ReactNode }) {
  return (
    <div className="px-4 py-1.5 text-[11px] font-medium uppercase tracking-wide text-text-secondary bg-surface-2/40 border-b border-border/50 sticky top-0 z-10">
      {children}
    </div>
  )
}

export function BansView({ serverId, status }: BansViewProps) {
  const { success, error } = useNotifications()
  const qc = useQueryClient()
  const [search, setSearch] = useState('')
  const [addOpen, setAddOpen] = useState(false)

  const isOnline = status === 'online'

  const { data: bans, isLoading } = useQuery({
    queryKey: ['bans', serverId],
    queryFn: () => api.players.bans(serverId),
    // Bans change rarely; poll gently while online so a live /ban shows up.
    refetchInterval: isOnline ? 10_000 : false,
  })

  const action = useMutation({
    mutationFn: (vars: { action: PlayerActionKind; name?: string; ip?: string; reason?: string }) =>
      api.players.action(serverId, vars),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['bans', serverId] })
      // The roster stamps player-ban status, so refresh it too.
      qc.invalidateQueries({ queryKey: ['players', serverId] })
    },
    onError: (e: Error) => error('Action failed', e.message),
  })

  const pardonPlayer = (ban: BannedPlayer) => {
    action.mutate(
      { action: 'pardon', name: ban.name },
      { onSuccess: () => success('Pardoned', ban.name) },
    )
  }
  const pardonIP = (ban: BannedIP) => {
    action.mutate(
      { action: 'pardon_ip', ip: ban.ip },
      { onSuccess: () => success('IP pardoned', ban.ip) },
    )
  }
  const banIP = (ip: string, reason: string) => {
    action.mutate(
      { action: 'ban_ip', ip, reason: reason || undefined },
      {
        onSuccess: () => {
          success('IP banned', ip)
          setAddOpen(false)
        },
      },
    )
  }

  const { players, ips } = useMemo(() => {
    const q = search.trim().toLowerCase()
    const players = (bans?.players ?? []).filter(
      (b) => !q || b.name.toLowerCase().includes(q) || (b.reason ?? '').toLowerCase().includes(q),
    )
    const ips = (bans?.ips ?? []).filter(
      (b) => !q || b.ip.toLowerCase().includes(q) || (b.reason ?? '').toLowerCase().includes(q),
    )
    return { players, ips }
  }, [bans, search])

  const total = (bans?.players.length ?? 0) + (bans?.ips.length ?? 0)
  const noMatches = players.length === 0 && ips.length === 0

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="flex-shrink-0 flex items-center justify-between gap-3 px-4 py-3 border-b border-border bg-surface">
        <div className="min-w-0">
          <h3 className="text-sm font-medium text-text-primary">
            {bans?.players.length ?? 0} player {(bans?.players.length ?? 0) === 1 ? 'ban' : 'bans'}
            <span className="text-text-secondary font-normal">
              {' · '}
              {bans?.ips.length ?? 0} IP {(bans?.ips.length ?? 0) === 1 ? 'ban' : 'bans'}
            </span>
          </h3>
          <p className="text-xs text-text-secondary truncate">
            {isOnline
              ? 'Applied live to the running server'
              : `Server is ${status} · edits banned-players.json / banned-ips.json`}
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={() => setAddOpen(true)}>
          <Plus className="h-3.5 w-3.5" /> Ban IP
        </Button>
      </div>

      {/* Search */}
      <div className="flex-shrink-0 px-4 py-2.5 border-b border-border bg-surface">
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-text-secondary" />
          <Input
            placeholder="Search bans…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-8"
          />
        </div>
      </div>

      {/* List */}
      <div className="flex-1 min-h-0 overflow-y-auto">
        {isLoading ? (
          <div className="flex justify-center py-8">
            <Loader2 className="h-5 w-5 animate-spin text-accent" />
          </div>
        ) : total === 0 ? (
          <div className="text-center py-12 text-text-secondary">
            <Ban className="h-8 w-8 mx-auto mb-2 opacity-30" />
            <p className="text-sm">No bans</p>
            <p className="text-xs mt-1">No players or IP addresses are banned.</p>
          </div>
        ) : noMatches ? (
          <div className="text-center py-12 text-text-secondary">
            <Search className="h-7 w-7 mx-auto mb-2 opacity-30" />
            <p className="text-sm">No matches</p>
            <p className="text-xs mt-1">Try a different search.</p>
          </div>
        ) : (
          <>
            {players.length > 0 && (
              <>
                <SectionHeader>
                  <span className="inline-flex items-center gap-1.5">
                    <User className="h-3 w-3" /> Player bans — {players.length}
                  </span>
                </SectionHeader>
                {players.map((b) => (
                  <PlayerBanRow
                    key={b.uuid || b.name}
                    ban={b}
                    busy={action.isPending}
                    onPardon={() => pardonPlayer(b)}
                  />
                ))}
              </>
            )}
            {ips.length > 0 && (
              <>
                <SectionHeader>
                  <span className="inline-flex items-center gap-1.5">
                    <Globe className="h-3 w-3" /> IP bans — {ips.length}
                  </span>
                </SectionHeader>
                {ips.map((b) => (
                  <IPBanRow
                    key={b.ip}
                    ban={b}
                    busy={action.isPending}
                    onPardon={() => pardonIP(b)}
                  />
                ))}
              </>
            )}
          </>
        )}
      </div>

      <AddIPBanDialog
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onSubmit={banIP}
        busy={action.isPending}
      />
    </div>
  )
}
