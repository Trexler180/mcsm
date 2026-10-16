import { describe, expect, it } from 'vitest'
import { niceTicks } from './charts'

describe('niceTicks', () => {
  it('rounds to friendly steps', () => {
    expect(niceTicks(0, 100, 4)).toEqual([0, 20, 40, 60, 80, 100])
    expect(niceTicks(0, 20, 3)).toEqual([0, 5, 10, 15, 20])
  })
  it('keeps counts whole', () => {
    expect(niceTicks(0, 1, 3, true)).toEqual([0, 1])
    expect(niceTicks(0, 3, 3, true)).toEqual([0, 1, 2, 3])
  })
})
