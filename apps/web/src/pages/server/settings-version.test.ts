import { describe, expect, it } from 'vitest'
import { javaFor } from './settings-version'

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
