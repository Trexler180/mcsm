import { describe, expect, it } from 'vitest'
import { modInitial } from './mod-icon'

describe('modInitial', () => {
  it('skips brackets and punctuation', () => {
    expect(modInitial('[26.2 Fabric] Alternate Current')).toBe('2')
    expect(modInitial('  lithium')).toBe('L')
    expect(modInitial('[]')).toBe('?')
  })
})
