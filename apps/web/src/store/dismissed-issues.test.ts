import { describe, expect, it } from 'vitest'
import { pruneDismissed, splitDismissed } from './dismissed-issues'

describe('splitDismissed', () => {
  it('hides an issue only while it is in the state it was dismissed at', () => {
    const issues = [
      { key: 'bka', fp: 'failed:2026-11-10' },
      { key: 'bkb', fp: 'failed:2026-11-12' },
      { key: 'tpsc' },
    ]
    const { shown, hidden } = splitDismissed(issues, { bka: 'failed:2026-11-10', bkb: 'failed:2026-11-01', tpsc: '' })
    expect(hidden.map((i) => i.key)).toEqual(['bka', 'tpsc'])
    expect(shown.map((i) => i.key)).toEqual(['bkb'])
  })
})

describe('pruneDismissed', () => {
  it('forgets issues that have cleared', () => {
    expect(pruneDismissed({ a: '', b: 'x' }, [{ key: 'b' }])).toEqual({ b: 'x' })
  })

  it('keeps dismissals whose source has not loaded', () => {
    expect(pruneDismissed({ reqr1: 'r1', tpsa: '' }, [], (k) => !k.startsWith('req'))).toEqual({ reqr1: 'r1' })
  })

  it('returns the same object when nothing changed', () => {
    const d = { a: '' }
    expect(pruneDismissed(d, [{ key: 'a' }])).toBe(d)
  })
})
