import { type ReactNode } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as Tooltip from '@radix-ui/react-tooltip'
import {
  Heart,
  Utensils,
  Sparkles,
  Gamepad2,
  Globe,
  MapPin,
  Loader2,
  Clock,
  RefreshCw,
  Copy,
  Skull,
  Swords,
  Footprints,
  Crown,
  Shield,
  Ban,
} from 'lucide-react'
import { Dialog } from '@/components/ui/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { useNotifications } from '@/store/notifications'
import type { ItemStack, PlayerDetail, PlayerStats } from '@/lib/types'
import {
  itemLabel,
  itemAbbr,
  itemTileClasses,
  enchantLabel,
  maxDurability,
} from './items'

function avatarUrl(name: string) {
  return `https://mc-heads.net/avatar/${encodeURIComponent(name)}/64`
}

const GAME_MODES = ['Survival', 'Creative', 'Adventure', 'Spectator']

function formatRelative(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime()
  const min = Math.floor(ms / 60000)
  if (min < 1) return 'just now'
  if (min < 60) return `${min}m ago`
  const hrs = Math.floor(min / 60)
  if (hrs < 24) return `${hrs}h ago`
  return `${Math.floor(hrs / 24)}d ago`
}

function formatPlaytime(ticks: number): string {
  const totalMin = Math.floor(ticks / 20 / 60)
  const days = Math.floor(totalMin / 1440)
  const hrs = Math.floor((totalMin % 1440) / 60)
  const min = totalMin % 60
  if (days > 0) return `${days}d ${hrs}h`
  if (hrs > 0) return `${hrs}h ${min}m`
  return `${min}m`
}

function formatDistance(cm: number): string {
  const m = cm / 100
  if (m >= 1000) return `${(m / 1000).toFixed(1)} km`
  return `${Math.round(m)} m`
}

function durabilityColor(pct: number): string {
  if (pct > 0.5) return 'bg-green-500'
  if (pct > 0.25) return 'bg-yellow-500'
  return 'bg-red-500'
}

function ItemTile({ item, highlight }: { item?: ItemStack; highlight?: boolean }) {
  if (!item) {
    return (
      <div
        className={`aspect-square rounded border ${
          highlight ? 'border-accent' : 'border-border/40'
        } bg-surface-2/30`}
      />
    )
  }

  const enchanted = (item.enchantments?.length ?? 0) > 0
  const max = maxDurability(item.id)
  const used = item.damage ?? 0
  const remaining = max !== undefined ? Math.max(0, max - used) : undefined
  const durPct = max !== undefined && max > 0 ? (remaining as number) / max : undefined
  const showDur = durPct !== undefined && used > 0

  return (
    <Tooltip.Root delayDuration={120}>
      <Tooltip.Trigger asChild>
        <div
          className={`relative aspect-square rounded border flex items-center justify-center text-[10px] font-semibold uppercase select-none ${
            highlight ? 'border-accent' : 'border-transparent'
          } ${itemTileClasses(item.id, enchanted)}`}
        >
          <span className="max-w-full truncate px-0.5">{itemAbbr(item.id)}</span>

          {enchanted && (
            <Sparkles className="absolute top-0.5 left-0.5 h-2.5 w-2.5 text-fuchsia-300/90" />
          )}
          {item.custom_name && (
            <span className="absolute top-0.5 right-0.5 h-1.5 w-1.5 rounded-full bg-amber-300" />
          )}
          {item.count > 1 && (
            <span className="absolute bottom-0.5 right-1 text-[10px] font-bold text-white drop-shadow">
              {item.count}
            </span>
          )}
          {showDur && (
            <span className="absolute inset-x-0.5 bottom-0.5 h-1 rounded-full bg-black/50 overflow-hidden">
              <span
                className={`block h-full ${durabilityColor(durPct as number)}`}
                style={{ width: `${Math.round((durPct as number) * 100)}%` }}
              />
            </span>
          )}
        </div>
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content
          side="top"
          sideOffset={6}
          className="z-[60] max-w-[16rem] rounded-md border border-border bg-surface px-3 py-2 text-xs shadow-xl"
        >
          {item.custom_name ? (
            <>
              <p className="font-semibold text-amber-300 italic">{item.custom_name}</p>
              <p className="text-text-secondary">{itemLabel(item.id)}</p>
            </>
          ) : (
            <p className="font-semibold text-text-primary">{itemLabel(item.id)}</p>
          )}
          {item.count > 1 && <p className="text-text-secondary">Count: {item.count}</p>}
          {showDur && (
            <p className="text-text-secondary">
              Durability: {remaining} / {max}
            </p>
          )}
          {enchanted && (
            <ul className="mt-1 space-y-0.5">
              {item.enchantments!.map((e) => (
                <li key={e.id} className="text-fuchsia-300">
                  {enchantLabel(e.id, e.level)}
                </li>
              ))}
            </ul>
          )}
          <Tooltip.Arrow className="fill-border" />
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  )
}

function SlotRange({
  bySlot,
  from,
  to,
  selected,
}: {
  bySlot: Map<number, ItemStack>
  from: number
  to: number
  selected?: number
}) {
  const slots = []
  for (let s = from; s <= to; s++) {
    slots.push(<ItemTile key={s} item={bySlot.get(s)} highlight={s === selected} />)
  }
  // Minecraft inventory rows are intrinsically 9-wide; cap the tile size so the
  // full row fits on phones instead of overflowing the dialog.
  return <div className="grid grid-cols-9 gap-1 max-w-[22rem] sm:max-w-none">{slots}</div>
}

function StatChip({
  icon,
  label,
  value,
}: {
  icon: ReactNode
  label: string
  value: string
}) {
  return (
    <div className="flex items-center gap-2 rounded border border-border/60 bg-surface-2/40 px-3 py-2">
      <div className="text-text-secondary">{icon}</div>
      <div className="min-w-0">
        <p className="text-[10px] uppercase tracking-wide text-text-secondary">{label}</p>
        <p className="text-sm font-medium text-text-primary truncate">{value}</p>
      </div>
    </div>
  )
}

function hasStats(s?: PlayerStats | null): s is PlayerStats {
  if (!s) return false
  return Object.values(s).some((v) => typeof v === 'number' && v > 0)
}

function StatsSection({ stats }: { stats?: PlayerStats | null }) {
  if (!hasStats(stats)) {
    return (
      <div>
        <h4 className="text-xs font-medium uppercase tracking-wide text-text-secondary mb-2">
          Lifetime stats
        </h4>
        <p className="text-xs text-text-secondary">No statistics recorded yet.</p>
      </div>
    )
  }
  const chips: { icon: ReactNode; label: string; value: string }[] = []
  if (stats.play_time_ticks)
    chips.push({
      icon: <Clock className="h-4 w-4 text-cyan-400" />,
      label: 'Playtime',
      value: formatPlaytime(stats.play_time_ticks),
    })
  if (stats.deaths)
    chips.push({
      icon: <Skull className="h-4 w-4 text-red-400" />,
      label: 'Deaths',
      value: String(stats.deaths),
    })
  if (stats.player_kills)
    chips.push({
      icon: <Swords className="h-4 w-4 text-orange-400" />,
      label: 'Player kills',
      value: String(stats.player_kills),
    })
  if (stats.mob_kills)
    chips.push({
      icon: <Swords className="h-4 w-4 text-green-400" />,
      label: 'Mob kills',
      value: String(stats.mob_kills),
    })
  if (stats.walked_cm)
    chips.push({
      icon: <Footprints className="h-4 w-4 text-blue-400" />,
      label: 'Distance walked',
      value: formatDistance(stats.walked_cm),
    })
  if (stats.jumps)
    chips.push({
      icon: <Sparkles className="h-4 w-4 text-purple-400" />,
      label: 'Jumps',
      value: stats.jumps.toLocaleString(),
    })

  return (
    <div>
      <h4 className="text-xs font-medium uppercase tracking-wide text-text-secondary mb-2">
        Lifetime stats
      </h4>
      <div className="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-6 gap-2">
        {chips.map((c) => (
          <StatChip key={c.label} {...c} />
        ))}
      </div>
    </div>
  )
}

function StalenessBanner({
  d,
  serverOnline,
  onRefresh,
  refreshing,
}: {
  d: PlayerDetail
  serverOnline: boolean
  onRefresh: () => void
  refreshing: boolean
}) {
  const live = serverOnline && d.online
  return (
    <div
      className={`flex items-start gap-2.5 rounded-md border px-3 py-2 text-xs ${
        live
          ? 'border-yellow-800/50 bg-yellow-900/20 text-yellow-200'
          : 'border-border/60 bg-surface-2/40 text-text-secondary'
      }`}
    >
      <Clock className="h-4 w-4 flex-shrink-0 mt-0.5" />
      <div className="flex-1 min-w-0">
        <p>
          Snapshot saved{' '}
          <span className="font-medium">
            {d.snapshot_at ? formatRelative(d.snapshot_at) : 'unknown'}
          </span>
          {d.snapshot_at && (
            <span className="text-text-secondary"> ({new Date(d.snapshot_at).toLocaleString()})</span>
          )}
        </p>
        {live && (
          <p className="mt-0.5">
            Player is online — health, position and inventory below may differ from live until the
            next world save.
          </p>
        )}
      </div>
      {serverOnline && (
        <Button
          size="sm"
          variant="outline"
          onClick={onRefresh}
          loading={refreshing}
          title="Flush the world to disk and reload"
        >
          <RefreshCw className="h-3.5 w-3.5" /> Refresh
        </Button>
      )}
    </div>
  )
}

function DetailBody({
  d,
  serverId,
  serverOnline,
}: {
  d: PlayerDetail
  serverId: string
  serverOnline: boolean
}) {
  const { success, error } = useNotifications()
  const qc = useQueryClient()

  const bySlot = new Map<number, ItemStack>()
  for (const it of d.inventory) bySlot.set(it.slot, it)
  const enderBySlot = new Map<number, ItemStack>()
  for (const it of d.ender_chest) enderBySlot.set(it.slot, it)

  const armor = [103, 102, 101, 100] // helmet, chest, legs, boots
  const dimension = d.dimension.replace(/^minecraft:/, '').replace(/_/g, ' ')
  const [x, y, z] = d.pos.length === 3 ? d.pos : [0, 0, 0]

  const refresh = useMutation({
    mutationFn: async () => {
      await api.servers.command(serverId, 'save-all flush')
      // Give the server a moment to flush playerdata to disk before re-reading.
      await new Promise((r) => setTimeout(r, 900))
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['player-detail', serverId, d.uuid] })
      success('Refreshed', 'Flushed the world and reloaded the snapshot')
    },
    onError: (e: Error) => error('Refresh failed', e.message),
  })

  const copy = async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text)
      success(`${label} copied`, text)
    } catch {
      error('Copy failed', 'Clipboard is unavailable')
    }
  }

  return (
    // The Dialog already scrolls its body, so don't nest a second scroll
    // container here — just lay the sections out and let the dialog handle
    // overflow on short viewports.
    <div className="space-y-5">
      {/* Identity / state row */}
      <div className="flex flex-wrap items-center gap-2">
        {d.bedrock && (
          <Badge
            className="bg-cyan-900/40 text-cyan-300 border border-cyan-800/50"
            title="Bedrock Edition (via Geyser)"
          >
            <Gamepad2 className="h-3 w-3" /> Bedrock
          </Badge>
        )}
        {d.op && <Badge variant="warning"><Crown className="h-3 w-3" /> Operator</Badge>}
        {d.whitelisted && <Badge variant="success"><Shield className="h-3 w-3" /> Whitelisted</Badge>}
        {d.banned && (
          <Badge variant="error" title={d.ban_reason || undefined}>
            <Ban className="h-3 w-3" /> Banned
          </Badge>
        )}
        <div className="flex-1" />
        <Button size="sm" variant="ghost" onClick={() => copy(d.uuid, 'UUID')}>
          <Copy className="h-3.5 w-3.5" /> UUID
        </Button>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => copy(`${Math.round(x)} ${Math.round(y)} ${Math.round(z)}`, 'Coordinates')}
        >
          <MapPin className="h-3.5 w-3.5" /> Coords
        </Button>
      </div>

      <StalenessBanner
        d={d}
        serverOnline={serverOnline}
        onRefresh={() => refresh.mutate()}
        refreshing={refresh.isPending}
      />

      {/* Vitals */}
      <div className="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-6 gap-2">
        <StatChip
          icon={<Heart className="h-4 w-4 text-red-400" />}
          label="Health"
          value={`${Math.round(d.health)} / ${Math.round(d.max_health)}`}
        />
        <StatChip
          icon={<Utensils className="h-4 w-4 text-orange-400" />}
          label="Food"
          value={`${d.food} / 20`}
        />
        <StatChip
          icon={<Sparkles className="h-4 w-4 text-green-400" />}
          label="XP"
          value={`Lvl ${d.xp_level}`}
        />
        <StatChip
          icon={<Gamepad2 className="h-4 w-4 text-blue-400" />}
          label="Mode"
          value={GAME_MODES[d.game_mode] ?? `#${d.game_mode}`}
        />
        <StatChip
          icon={<Globe className="h-4 w-4 text-purple-400" />}
          label="Dimension"
          value={dimension}
        />
        <StatChip
          icon={<MapPin className="h-4 w-4 text-yellow-400" />}
          label="Position"
          value={`${Math.round(x)}, ${Math.round(y)}, ${Math.round(z)}`}
        />
      </div>

      {/* Inventory */}
      <div>
        <h4 className="text-xs font-medium uppercase tracking-wide text-text-secondary mb-2">
          Inventory
        </h4>
        <div className="grid gap-4 lg:grid-cols-[auto_minmax(0,1fr)]">
          <div className="flex gap-4">
            <div>
              <p className="text-[10px] text-text-secondary mb-1">Armor</p>
              <div className="grid grid-cols-1 gap-1 w-10">
                {armor.map((s) => (
                  <ItemTile key={s} item={bySlot.get(s)} />
                ))}
              </div>
            </div>
            <div>
              <p className="text-[10px] text-text-secondary mb-1">Off-hand</p>
              <div className="w-10">
                <ItemTile item={bySlot.get(-106)} />
              </div>
            </div>
          </div>

          <div className="min-w-0 space-y-1">
            <SlotRange bySlot={bySlot} from={9} to={35} />
            <div className="pt-1">
              <SlotRange bySlot={bySlot} from={0} to={8} selected={d.selected_slot} />
            </div>
          </div>
        </div>
      </div>

      {/* Ender chest */}
      <div>
        <h4 className="text-xs font-medium uppercase tracking-wide text-text-secondary mb-2">
          Ender Chest
        </h4>
        {d.ender_chest.length === 0 ? (
          <p className="text-xs text-text-secondary">Empty</p>
        ) : (
          <SlotRange bySlot={enderBySlot} from={0} to={26} />
        )}
      </div>

      {/* Lifetime stats */}
      <StatsSection stats={d.stats} />
    </div>
  )
}

export function PlayerDetailDialog({
  serverId,
  player,
  serverOnline,
  open,
  onClose,
}: {
  serverId: string
  player: { name: string; uuid?: string } | null
  serverOnline: boolean
  open: boolean
  onClose: () => void
}) {
  const uuid = player?.uuid
  const { data, isLoading, error } = useQuery({
    queryKey: ['player-detail', serverId, uuid],
    queryFn: () => api.players.get(serverId, uuid!),
    enabled: open && !!uuid,
  })

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={player?.name ?? 'Player'}
      description={uuid}
      className="!max-w-5xl"
    >
      {!uuid ? (
        <p className="text-sm text-text-secondary py-6 text-center">
          No saved data for this player yet.
        </p>
      ) : isLoading ? (
        <div className="flex justify-center py-10">
          <Loader2 className="h-5 w-5 animate-spin text-accent" />
        </div>
      ) : error ? (
        <p className="text-sm text-red-400 py-6 text-center">{(error as Error).message}</p>
      ) : data ? (
        <Tooltip.Provider delayDuration={120}>
          <div className="flex flex-col gap-4 sm:flex-row sm:items-start">
            {data.bedrock ? (
              <div
                className="h-14 w-14 flex-shrink-0 flex items-center justify-center rounded bg-gradient-to-br from-cyan-700/50 to-teal-800/50 text-cyan-200"
                title="Bedrock Edition player"
              >
                <Gamepad2 className="h-6 w-6" />
              </div>
            ) : (
              <img
                src={avatarUrl(data.name)}
                alt=""
                className="h-14 w-14 rounded bg-surface-2 flex-shrink-0"
                onError={(e) => {
                  ;(e.target as HTMLImageElement).style.visibility = 'hidden'
                }}
              />
            )}
            <div className="flex-1 min-w-0">
              <DetailBody d={data} serverId={serverId} serverOnline={serverOnline} />
            </div>
          </div>
        </Tooltip.Provider>
      ) : null}
    </Dialog>
  )
}
