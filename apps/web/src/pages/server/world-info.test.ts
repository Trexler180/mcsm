import { describe, expect, it } from 'vitest'
import { TAG_BYTE, TAG_COMPOUND, TAG_INT, TAG_INT_ARRAY, TAG_LONG, TAG_STRING, type NbtEntry, type NbtTag } from '@/lib/nbt'
import { parseWorldInfo, ticksToClock } from './world-info'

const s = (value: string): NbtTag => ({ type: TAG_STRING, value })
const i = (value: number): NbtTag => ({ type: TAG_INT, value })
const b = (value: number): NbtTag => ({ type: TAG_BYTE, value })
const l = (value: bigint): NbtTag => ({ type: TAG_LONG, value })
const c = (fields: Record<string, NbtTag>): NbtTag => ({
  type: TAG_COMPOUND,
  value: Object.entries(fields).map(([name, tag]): NbtEntry => ({ name, tag })),
})
const level = (data: Record<string, NbtTag>) => c({ Data: c(data) })

describe('parseWorldInfo', () => {
  it('reads a 1.21 level.dat', () => {
    const info = parseWorldInfo(
      level({
        LevelName: s('world'),
        Version: c({ Name: s('1.21.4'), Snapshot: b(0) }),
        WorldGenSettings: c({
          seed: l(-4172144997902289642n),
          dimensions: c({ 'minecraft:overworld': c({ generator: c({ type: s('minecraft:noise'), settings: s('minecraft:amplified') }) }) }),
        }),
        SpawnX: i(16),
        SpawnY: i(72),
        SpawnZ: i(-48),
        Time: l(240000n),
        LastPlayed: l(1760000000000n),
        hardcore: b(1),
        GameRules: c({ keepInventory: s('true'), randomTickSpeed: s('3'), doDaylightCycle: s('false') }),
      }),
    )
    expect(info).toMatchObject({
      levelName: 'world',
      version: '1.21.4',
      snapshot: false,
      seed: '-4172144997902289642',
      type: 'Amplified',
      spawn: [16, 72, -48],
      day: 10,
      lastPlayed: 1760000000000,
      hardcore: true,
      gameRules: { keepInventory: true, randomTickSpeed: 3, doDaylightCycle: false },
    })
  })

  it('reads the newer spawn compound and namespaced rules', () => {
    const info = parseWorldInfo(
      level({
        spawn: c({ dimension: s('minecraft:overworld'), pos: { type: TAG_INT_ARRAY, value: [1, 64, 2] } }),
        game_rules: c({ 'minecraft:keep_inventory': b(1), 'minecraft:random_tick_speed': i(3) }),
        WorldGenSettings: c({ dimensions: c({ 'minecraft:overworld': c({ generator: c({ type: s('minecraft:flat') }) }) }) }),
      }),
    )
    expect(info.spawn).toEqual([1, 64, 2])
    expect(info.type).toBe('Flat')
    expect(info.gameRules).toEqual({ 'minecraft:keep_inventory': true, 'minecraft:random_tick_speed': 3 })
  })

  it('falls back to the old generator name and seed', () => {
    const info = parseWorldInfo(level({ generatorName: s('largeBiomes'), RandomSeed: l(42n) }))
    expect(info.type).toBe('Large biomes')
    expect(info.seed).toBe('42')
  })

  it('copes with a file that isn’t a level', () => {
    expect(parseWorldInfo(c({}))).toEqual({ gameRules: {} })
  })
})

describe('world state', () => {
  it('reads game mode, cheats, the clock, weather and the data version', () => {
    const info = parseWorldInfo(level({ GameType: i(1), allowCommands: b(1), DayTime: l(18000n), raining: b(1), thundering: b(0), DataVersion: i(4189) }))
    expect(info).toMatchObject({ gameMode: 'Creative', cheats: true, clock: '00:00', weather: 'Raining', dataVersion: 4189 })
  })
  it('leaves out what the save doesn’t say', () => {
    const info = parseWorldInfo(level({}))
    expect(info.gameMode).toBeUndefined()
    expect(info.weather).toBeUndefined()
    expect(info.cheats).toBe(false)
  })
  it('starts the clock at six in the morning', () => {
    expect(ticksToClock(0)).toBe('06:00')
    expect(ticksToClock(6000)).toBe('12:00')
    expect(ticksToClock(24000 * 3 + 13000)).toBe('19:00')
  })
})
