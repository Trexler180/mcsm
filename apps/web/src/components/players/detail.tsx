import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  Heart,
  Utensils,
  Sparkles,
  Gamepad2,
  Globe,
  MapPin,
  Loader2,
} from 'lucide-react'
import { Dialog } from '@/components/ui/dialog'
import { api } from '@/lib/api'
import type { ItemStack, PlayerDetail } from '@/lib/types'

function avatarUrl(name: string) {
  return `https://mc-heads.net/avatar/${encodeURIComponent(name)}/64`
}

const GAME_MODES = ['Survival', 'Creative', 'Adventure', 'Spectator']

// "minecraft:diamond_sword" -> "Diamond Sword"
function itemLabel(id: string): string {
  return id
    .replace(/^minecraft:/, '')
    .split('_')
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(' ')
}

// A short token for the slot tile (first letters of each word, max 3 chars).
function itemAbbr(id: string): string {
  const name = id.replace(/^minecraft:/, '')
  const parts = name.split('_')
  if (parts.length === 1) return name.slice(0, 3)
  return parts
    .map((p) => p[0])
    .join('')
    .slice(0, 3)
}

function Slot({
  item,
  highlight,
}: {
  item?: ItemStack
  highlight?: boolean
}) {
  return (
    <div
      className={`relative aspect-square rounded border flex items-center justify-center text-[10px] font-medium select-none ${
        highlight ? 'border-accent' : 'border-border/60'
      } ${item ? 'bg-surface-2 text-text-primary' : 'bg-surface-2/30 text-transparent'}`}
      title={item ? `${itemLabel(item.id)} ×${item.count}` : undefined}
    >
      {item && <span className="max-w-full truncate px-0.5 uppercase">{itemAbbr(item.id)}</span>}
      {item && item.count > 1 && (
        <span className="absolute bottom-0 right-0.5 text-[10px] font-bold text-white drop-shadow">
          {item.count}
        </span>
      )}
    </div>
  )
}

// Render a fixed run of slots [from..to] from a slot->item map.
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
    slots.push(
      <Slot key={s} item={bySlot.get(s)} highlight={s === selected} />,
    )
  }
  return <div className="grid grid-cols-9 gap-1">{slots}</div>
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
        <p className="text-[10px] uppercase tracking-wide text-text-secondary">
          {label}
        </p>
        <p className="text-sm font-medium text-text-primary truncate">{value}</p>
      </div>
    </div>
  )
}

function DetailBody({ d }: { d: PlayerDetail }) {
  const bySlot = new Map<number, ItemStack>()
  for (const it of d.inventory) bySlot.set(it.slot, it)

  const armor = [103, 102, 101, 100] // helmet, chest, legs, boots
  const dimension = d.dimension.replace(/^minecraft:/, '').replace(/_/g, ' ')
  const [x, y, z] = d.pos.length === 3 ? d.pos : [0, 0, 0]

  return (
    <div className="space-y-5 max-h-[calc(100vh-12rem)] overflow-y-auto pr-2">
      {/* Stats */}
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

      {/* Inventory: armor + offhand, then main grid, then hotbar */}
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
                  <Slot key={s} item={bySlot.get(s)} />
                ))}
              </div>
            </div>
            <div>
              <p className="text-[10px] text-text-secondary mb-1">Off-hand</p>
              <div className="w-10">
                <Slot item={bySlot.get(-106)} />
              </div>
            </div>
          </div>

          <div className="min-w-0 space-y-1">
            {/* Main storage: slots 9-35 */}
            <SlotRange bySlot={bySlot} from={9} to={35} />
            {/* Hotbar: slots 0-8, selected highlighted */}
            <div className="pt-1">
              <SlotRange
                bySlot={bySlot}
                from={0}
                to={8}
                selected={d.selected_slot}
              />
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
          <SlotRange
            bySlot={(() => {
              const m = new Map<number, ItemStack>()
              for (const it of d.ender_chest) m.set(it.slot, it)
              return m
            })()}
            from={0}
            to={26}
          />
        )}
      </div>
    </div>
  )
}

export function PlayerDetailDialog({
  serverId,
  player,
  open,
  onClose,
}: {
  serverId: string
  player: { name: string; uuid?: string } | null
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
        <p className="text-sm text-red-400 py-6 text-center">
          {(error as Error).message}
        </p>
      ) : data ? (
        <div className="flex flex-col gap-4 sm:flex-row sm:items-start">
          <img
            src={avatarUrl(data.name)}
            alt=""
            className="h-14 w-14 rounded bg-surface-2 flex-shrink-0"
            onError={(e) => {
              ;(e.target as HTMLImageElement).style.visibility = 'hidden'
            }}
          />
          <div className="flex-1 min-w-0">
            <DetailBody d={data} />
          </div>
        </div>
      ) : null}
    </Dialog>
  )
}
