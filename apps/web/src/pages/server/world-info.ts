import { TAG_BYTE, TAG_COMPOUND, TAG_INT_ARRAY, type NbtEntry, type NbtTag } from '@/lib/nbt'

// What a world's level.dat says about it. Fields move between Minecraft
// versions (spawn became a compound, game rules got namespaced keys), so each
// reader tries the layouts it knows and leaves out what it can't find.

export interface WorldInfo {
  /** The name the world was created with, which can differ from its folder. */
  levelName?: string
  /** The Minecraft version that last saved it. */
  version?: string
  snapshot?: boolean
  seed?: string
  /** Normal, Flat, Amplified, Large biomes, or the generator's own name. */
  type?: string
  spawn?: [number, number, number]
  /** In-game days since it was made. */
  day?: number
  /** When it was last saved by a running server, as epoch ms. */
  lastPlayed?: number
  hardcore?: boolean
  /** The game mode new players start in. */
  gameMode?: string
  /** Whether commands (cheats) are allowed in single player. */
  cheats?: boolean
  /** The time of day on a 24-hour clock, as of the last save. */
  clock?: string
  weather?: 'Clear' | 'Raining' | 'Thunderstorm'
  /** The save format's version number. */
  dataVersion?: number
  /** Game rules as saved: booleans, numbers, or the raw text. */
  gameRules: Record<string, boolean | number | string>
}

function entries(tag: NbtTag | undefined): NbtEntry[] | undefined {
  return tag?.type === TAG_COMPOUND ? (tag.value as NbtEntry[]) : undefined
}

function field(of: NbtEntry[] | undefined, name: string): NbtTag | undefined {
  return of?.find((e) => e.name === name)?.tag
}

function num(tag: NbtTag | undefined): number | undefined {
  if (typeof tag?.value === 'number') return tag.value
  if (typeof tag?.value === 'bigint') return Number(tag.value)
  return undefined
}

function big(tag: NbtTag | undefined): bigint | undefined {
  if (typeof tag?.value === 'bigint') return tag.value
  if (typeof tag?.value === 'number') return BigInt(Math.trunc(tag.value))
  return undefined
}

function str(tag: NbtTag | undefined): string | undefined {
  return typeof tag?.value === 'string' ? tag.value : undefined
}

const GENERATOR_NAMES: Record<string, string> = {
  default: 'Normal',
  'minecraft:overworld': 'Normal',
  flat: 'Flat',
  'minecraft:flat': 'Flat',
  largeBiomes: 'Large biomes',
  'minecraft:large_biomes': 'Large biomes',
  amplified: 'Amplified',
  'minecraft:amplified': 'Amplified',
  debug_all_block_states: 'Debug',
  'minecraft:debug': 'Debug',
}

function generatorLabel(name: string): string {
  if (GENERATOR_NAMES[name]) return GENERATOR_NAMES[name]
  const bare = name.replace(/^minecraft:/, '').replace(/_/g, ' ')
  return bare.charAt(0).toUpperCase() + bare.slice(1)
}

function worldType(data: NbtEntry[], gen: NbtEntry[] | undefined): string | undefined {
  const overworld = entries(field(entries(field(gen, 'dimensions')), 'minecraft:overworld'))
  const generator = entries(field(overworld, 'generator'))
  if (generator) {
    const kind = str(field(generator, 'type'))
    if (kind === 'minecraft:noise') {
      const settings = str(field(generator, 'settings'))
      return settings ? generatorLabel(settings) : 'Normal'
    }
    if (kind) return generatorLabel(kind)
  }
  const old = str(field(data, 'generatorName'))
  return old ? generatorLabel(old) : undefined
}

function spawnOf(data: NbtEntry[]): [number, number, number] | undefined {
  const x = num(field(data, 'SpawnX'))
  const y = num(field(data, 'SpawnY'))
  const z = num(field(data, 'SpawnZ'))
  if (x !== undefined && y !== undefined && z !== undefined) return [x, y, z]
  const pos = field(entries(field(data, 'spawn')), 'pos')
  if (pos?.type === TAG_INT_ARRAY && Array.isArray(pos.value) && pos.value.length === 3) return pos.value as [number, number, number]
  return undefined
}

function ruleValue(tag: NbtTag): boolean | number | string | undefined {
  if (typeof tag.value === 'string') {
    if (tag.value === 'true' || tag.value === 'false') return tag.value === 'true'
    return /^-?\d+$/.test(tag.value) ? Number(tag.value) : tag.value
  }
  // Newer saves type their rules: a byte is a yes/no rule.
  if (tag.type === TAG_BYTE && typeof tag.value === 'number') return tag.value !== 0
  if (typeof tag.value === 'number') return tag.value
  return undefined
}

function gameRulesOf(data: NbtEntry[]): Record<string, boolean | number | string> {
  const out: Record<string, boolean | number | string> = {}
  for (const e of entries(field(data, 'GameRules')) ?? entries(field(data, 'game_rules')) ?? []) {
    const v = ruleValue(e.tag)
    if (v !== undefined) out[e.name] = v
  }
  return out
}

const GAME_MODES = ['Survival', 'Creative', 'Adventure', 'Spectator']

/** The in-game time of day as a 24-hour clock: tick 0 is 06:00. */
export function ticksToClock(dayTime: number): string {
  const t = ((dayTime % 24000) + 24000) % 24000
  const minutes = Math.floor((((t / 1000 + 6) % 24) * 60))
  return `${String(Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`
}

function weatherOf(data: NbtEntry[]): WorldInfo['weather'] {
  const rain = num(field(data, 'raining'))
  if (num(field(data, 'thundering'))) return 'Thunderstorm'
  if (rain) return 'Raining'
  return rain === 0 ? 'Clear' : undefined
}

export function parseWorldInfo(root: NbtTag): WorldInfo {
  const data = entries(field(entries(root), 'Data'))
  if (!data) return { gameRules: {} }
  const version = entries(field(data, 'Version'))
  const gen = entries(field(data, 'WorldGenSettings'))
  const seed = big(field(gen, 'seed')) ?? big(field(data, 'RandomSeed'))
  const time = num(field(data, 'Time'))
  const last = num(field(data, 'LastPlayed'))
  const mode = num(field(data, 'GameType'))
  const dayTime = num(field(data, 'DayTime'))
  return {
    levelName: str(field(data, 'LevelName')),
    version: str(field(version, 'Name')),
    snapshot: num(field(version, 'Snapshot')) === 1,
    seed: seed?.toString(),
    type: worldType(data, gen),
    spawn: spawnOf(data),
    day: time !== undefined ? Math.floor(time / 24000) : undefined,
    lastPlayed: last && last > 0 ? last : undefined,
    hardcore: num(field(data, 'hardcore')) === 1,
    gameMode: mode !== undefined ? GAME_MODES[mode] : undefined,
    cheats: num(field(data, 'allowCommands')) === 1,
    clock: dayTime !== undefined ? ticksToClock(dayTime) : undefined,
    weather: weatherOf(data),
    dataVersion: num(field(data, 'DataVersion')),
    gameRules: gameRulesOf(data),
  }
}

/** Dimension folders inside a world folder, by the name players know. */
export const DIMENSIONS: Record<string, string> = { region: 'Overworld', 'DIM-1': 'Nether', DIM1: 'End' }

/** Folders Bukkit-family servers keep a world's Nether and End in, beside it. */
export function linkedFolders(world: string): string[] {
  return [`${world}_nether`, `${world}_the_end`]
}
