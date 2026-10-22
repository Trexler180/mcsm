import { describe, expect, it } from 'vitest'
import type { ModCompat } from '@/lib/types'
import { compatCounts, compatText, javaFor } from './settings-version'

describe('javaFor', () => {
  it('knows which Java each Minecraft needs', () => {
    expect(javaFor('1.21.4')).toBe(21)
    expect(javaFor('1.20.5')).toBe(21)
    expect(javaFor('1.20.4')).toBe(17)
    expect(javaFor('1.18.2')).toBe(17)
    expect(javaFor('1.17.1')).toBe(16)
    expect(javaFor('1.16.5')).toBe(8)
    expect(javaFor('25w41a')).toBe(21)
  })
})

const mod = (status: ModCompat['status'], enabled = true, target_version?: string): ModCompat => ({
  mod_id: status + enabled, name: 'm', source: 'modrinth', current_version: '1', status, enabled, pinned: false, target_version,
})

describe('compatText', () => {
  it('reads the API verdicts the right way round', () => {
    // "compatible" is swapped for another build; "supported" is left alone.
    expect(compatText(mod('compatible', true, '2.0'))).toBe('Updates to 2.0')
    expect(compatText(mod('supported'))).toBe('Works as it is')
  })
  it('says a turned-off mod stays off', () => {
    expect(compatText(mod('compatible', false, '2.0'))).toBe('Updates to 2.0, stays off')
    expect(compatText(mod('incompatible', false))).toBe('Nothing for this version yet, stays off')
  })
})

describe('compatCounts', () => {
  it('only counts running mods as turned off, and adds up to the list', () => {
    const mods = [mod('compatible'), mod('compatible', false, '2'), mod('supported'), mod('incompatible'), mod('incompatible', false), mod('unmanaged')]
    const c = compatCounts(mods)
    expect(c).toEqual({ updated: 2, unchanged: 2, turnedOff: 1, unchecked: 1 })
    expect(c.updated + c.unchanged + c.turnedOff + c.unchecked).toBe(mods.length)
  })
})
