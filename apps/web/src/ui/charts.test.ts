import { describe, expect, it } from 'vitest'
import { barTicks, niceTicks, peakBars, stepPath } from './charts'

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

describe('peakBars', () => {
  const H = 3_600_000
  const at = (h: number, m = 0) => new Date(2026, 9, 20, h, m).getTime()

  it('keeps the peak of each local hour', () => {
    const bars = peakBars(
      [
        { ts: at(10, 5), players: 1 },
        { ts: at(10, 40), players: 3 },
        { ts: at(11, 20), players: 2 },
      ],
      'players',
      200,
      H,
    )
    expect(bars.map((b) => [b.ts, b.v])).toEqual([
      [at(10), 3],
      [at(11), 2],
    ])
    expect(bars[0].span).toBe(H)
  })

  it('leaves gaps in as empty bars', () => {
    const bars = peakBars(
      [
        { ts: at(8), players: 4 },
        { ts: at(11), players: 1 },
      ],
      'players',
      200,
      H,
    )
    expect(bars.map((b) => b.v)).toEqual([4, 0, 0, 1])
  })

  it('widens the bars to stay under the limit', () => {
    const data = Array.from({ length: 7 * 24 }, (_, i) => ({ ts: at(0) + i * H, players: i % 5 }))
    const bars = peakBars(data, 'players', 48)
    expect(bars.length).toBeLessThan(48)
    expect(bars[0].span).toBe(6 * H)
    expect(Math.max(...bars.map((b) => b.v))).toBe(4)
  })

  it('goes down to ten-minute bars when the data is that fine', () => {
    const data = Array.from({ length: 360 }, (_, i) => ({ ts: at(0) + i * 240_000, players: i % 7 }))
    const bars = peakBars(data, 'players', 200, 240_000)
    expect(bars[0].span).toBe(H / 6)
    expect(bars.length).toBe(144)
  })

  it('never splits finer than the data', () => {
    const data = Array.from({ length: 7 * 24 }, (_, i) => ({ ts: at(0) + i * H, players: 1 }))
    expect(peakBars(data, 'players', 200, 0)[0].span).toBe(H)
    expect(peakBars(data, 'players', 200, 2 * H)[0].span).toBe(2 * H)
  })

  it('handles no data', () => {
    expect(peakBars([], 'players')).toEqual([])
  })
})

describe('barTicks', () => {
  const H = 3_600_000
  const at = (h: number, m = 0) => new Date(2026, 9, 20, h, m).getTime()

  it('labels a day of ten-minute bars every three hours', () => {
    const bars = peakBars([{ ts: at(0), players: 0 }, { ts: at(23, 59), players: 0 }], 'players')
    const labelled = bars.filter(barTicks(bars)).map((b) => new Date(b.ts).getHours())
    expect(labelled).toEqual([0, 3, 6, 9, 12, 15, 18, 21])
  })

  it('labels a week of hourly bars at midnight', () => {
    const bars = peakBars([{ ts: at(5), players: 0 }, { ts: at(5) + 7 * 24 * H, players: 0 }], 'players', 200, H)
    const labelled = bars.filter(barTicks(bars))
    expect(labelled.length).toBe(7)
    expect(labelled.every((b) => new Date(b.ts).getHours() === 0)).toBe(true)
  })
})

describe('stepPath', () => {
  it('traces one sharp-cornered staircase when r is 0', () => {
    expect(stepPath([50, 50, 20], 0, 10, 100, 0)).toBe('M0 100 L0 50 Q0 50 0 50 L20 50 Q20 50 20 50 L20 20 Q20 20 20 20 L30 20 Q30 20 30 20 L30 100 Z')
  })

  it('drops to the base for zero bars and back up after', () => {
    const d = stepPath([50, 100, 50], 0, 10, 100, 0)
    expect(d).toBe('M0 100 L0 50 Q0 50 0 50 L10 50 Q10 50 10 50 L10 100 Q10 100 10 100 L20 100 Q20 100 20 100 L20 50 Q20 50 20 50 L30 50 Q30 50 30 50 L30 100 Z')
  })

  it('rounds bends without overshooting short steps', () => {
    expect(stepPath([50, 49], 0, 10, 100, 3)).toBe('M0 100 L0 53 Q0 50 3 50 L7 50 Q10 50 10 49.5 L10 49.5 Q10 49 13 49 L17 49 Q20 49 20 52 L20 100 Z')
  })

  it('draws nothing for no bars', () => {
    expect(stepPath([], 0, 10, 100)).toBe('')
  })
})
