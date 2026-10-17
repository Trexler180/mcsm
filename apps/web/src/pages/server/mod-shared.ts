import type { ModProjectType, ModSearchHit, ModSource } from '@/lib/types'
import { plural } from '@/ui/util'

// What finding and installing mods share: how to describe a project's sides,
// whether it fits the server, and the bits of paging and labelling the
// browse list and the installed list both use.

export const SOURCE_LABEL: Record<ModSource, string> = { modrinth: 'Modrinth', curseforge: 'CurseForge', hangar: 'Hangar', spigotmc: 'SpigotMC' }

/** The Minecraft versions a release is for, short: the server's own when it's
 *  one of many. */
export function gameVersions(list: string[], mine: string): string {
  if (list.length <= 3) return list.join(', ') || 'any version'
  if (list.includes(mine)) return `${mine} and ${plural(list.length - 1, 'other')}`
  return `${list[0]} to ${list[list.length - 1]}`
}

/** Where a project has to be installed, as the browse filter asks Modrinth:
 *  '' anything that runs on a server, or one side in particular. */
export type Side = '' | 'server_only' | 'client_server' | 'client' | 'any'

export const SIDES: { value: Side; label: string }[] = [
  { value: '', label: 'Runs on the server' },
  { value: 'server_only', label: 'Server only' },
  { value: 'client_server', label: 'Server and players' },
  { value: 'client', label: 'Players’ games' },
  { value: 'any', label: 'Anywhere' },
]

type Sides = Pick<ModSearchHit, 'client_side' | 'server_side'>

const on = (v: string) => v === 'required' || v === 'optional'

/** A short note on where it runs, from what the project says about itself.
 *  Nothing when the source doesn't say (CurseForge, Hangar, SpigotMC). */
export function sideNote(h: Sides): { text: string; warn?: boolean } | null {
  if (h.server_side === 'unsupported' && on(h.client_side)) return { text: 'Players only. It does nothing on a server.', warn: true }
  if (h.client_side === 'unsupported' && on(h.server_side)) return { text: 'Server only.' }
  if (h.client_side === 'required') return { text: 'Players need it too.' }
  if (h.client_side === 'optional' && on(h.server_side)) return { text: 'Players can add it too.' }
  return null
}

/** Whether a result plainly contradicts the side asked for. Modrinth matches a
 *  project when any of its versions fits, so a few come back describing
 *  themselves otherwise; those are left out rather than shown as matches. */
export function contradicts(h: Sides, side: Side): boolean {
  if (!h.client_side && !h.server_side) return false
  switch (side) {
    case '':
      return h.server_side === 'unsupported'
    case 'server_only':
      return h.server_side === 'unsupported' || h.client_side === 'required'
    case 'client_server':
      return h.server_side === 'unsupported' || h.client_side === 'unsupported'
    case 'client':
      return h.client_side === 'unsupported'
    default:
      return false
  }
}

/** Whether a result says it's made for this server. Modrinth lists loaders
 *  among a project's categories; SpigotMC's version lists are too rough to
 *  judge by, and a project with no list isn't held against. */
export function fitsServer(h: Pick<ModSearchHit, 'versions' | 'categories'>, kind: ModProjectType, source: ModSource, platform: string, mc: string): boolean {
  const vers = h.versions ?? []
  if (source !== 'spigotmc' && mc && vers.length > 0 && !vers.includes(mc)) return false
  if (source === 'modrinth' && (kind === 'mod' || kind === 'modpack') && platform && !(h.categories ?? []).includes(platform)) return false
  return true
}

/** Up to five page numbers around the current one, 0-based. */
export function pageWindow(page: number, pages: number, span = 5): number[] {
  let start = Math.max(0, page - Math.floor(span / 2))
  const end = Math.min(pages - 1, start + span - 1)
  start = Math.max(0, end - span + 1)
  const out: number[] = []
  for (let i = start; i <= end; i++) out.push(i)
  return out
}

/** Mod loaders a browse can narrow to. */
export const LOADERS = ['fabric', 'forge', 'neoforge', 'quilt']

/** Loader and platform names Modrinth files among a project's categories,
 *  left out where categories describe what a project is. */
const LOADER_TAGS = new Set([...LOADERS, 'bukkit', 'spigot', 'paper', 'purpur', 'folia', 'sponge', 'velocity', 'bungeecord', 'waterfall', 'liteloader', 'modloader', 'rift', 'babric', 'datapack', 'minecraft', 'iris', 'optifine', 'canvas', 'vanilla'])

export const topicsOf = (categories: string[]) => categories.filter((c) => !LOADER_TAGS.has(c))
