import { describe, expect, it } from 'vitest'
import { suggestedName } from './schedules'

describe('suggestedName', () => {
  it('names a schedule after what it does and how often', () => {
    expect(suggestedName('backup', 'daily')).toBe('Nightly backup')
    expect(suggestedName('restart', 'weekly')).toBe('Weekly restart')
    expect(suggestedName('mod_update', 'monthly')).toBe('Monthly mod update')
  })
})
