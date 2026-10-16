import { describe, expect, it } from 'vitest'
import { gameVersions, modsViewOf } from './mods'

describe('gameVersions', () => {
  it('lists a few in full', () => {
    expect(gameVersions(['1.21.3', '1.21.4'], '1.21.4')).toBe('1.21.3, 1.21.4')
    expect(gameVersions([], '1.21.4')).toBe('any version')
  })
  it('names the server’s version among many', () => {
    expect(gameVersions(['1.21.3', '1.21.4', '1.21.5', '26.1'], '1.21.4')).toBe('1.21.4 and 3 others')
  })
  it('gives the range when the server’s isn’t there', () => {
    expect(gameVersions(['1.20', '1.20.1', '1.20.2', '1.20.4'], '1.21.4')).toBe('1.20 to 1.20.4')
  })
})

describe('modsViewOf', () => {
  it('defaults to what’s installed', () => {
    expect(modsViewOf(undefined)).toBe('installed')
    expect(modsViewOf('nonsense')).toBe('installed')
    expect(modsViewOf('browse')).toBe('browse')
    expect(modsViewOf('configs')).toBe('configs')
  })
})
