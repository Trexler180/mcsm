import { describe, expect, it } from 'vitest'
import { cronOf, describeCron, fiveMinutesBefore, nextRun, nextRuns, parseCron, whenOf } from './cron'

describe('describeCron', () => {
  it('says schedules the way people do', () => {
    expect(describeCron('0 4 * * *')).toBe('Every day at 04:00')
    expect(describeCron('30 6 * * 1-5')).toBe('Weekdays at 06:30')
    expect(describeCron('0 12 * * 0')).toBe('Every Sunday at 12:00')
    expect(describeCron('*/15 * * * *')).toBe('Every 15 minutes')
    expect(describeCron('0 * * * *')).toBe('Every hour, on the hour')
    expect(describeCron('0 3 1 * *')).toBe('Monthly on the 1st at 03:00')
    expect(describeCron('0 3 22 * *')).toBe('Monthly on the 22nd at 03:00')
    expect(describeCron('nonsense')).toBe('Invalid schedule')
  })
})

describe('nextRun', () => {
  it('steps to the next matching minute', () => {
    const from = new Date(2026, 9, 16, 3, 30).getTime()
    expect(new Date(nextRun('0 4 * * *', from) as number)).toEqual(new Date(2026, 9, 16, 4, 0))
    const after = new Date(2026, 9, 16, 4, 0).getTime()
    expect(new Date(nextRun('0 4 * * *', after) as number)).toEqual(new Date(2026, 9, 17, 4, 0))
  })
  it('honours weekdays', () => {
    // Friday 16 Oct 2026, 23:00: the next weekday 06:30 is Monday.
    const from = new Date(2026, 9, 16, 23, 0).getTime()
    expect(new Date(nextRun('30 6 * * 1-5', from) as number)).toEqual(new Date(2026, 9, 19, 6, 30))
  })
  it('lists the next few', () => {
    expect(nextRuns('0 * * * *', 3, new Date(2026, 0, 1, 10, 5).getTime()).map((t) => new Date(t).getHours())).toEqual([11, 12, 13])
  })
  it('rejects what cron would', () => {
    expect(parseCron('60 * * * *')).toBeNull()
    expect(parseCron('* * *')).toBeNull()
  })
})

describe('the editor round trip', () => {
  it('reads and writes the friendly shapes', () => {
    expect(whenOf('15 5 * * *')).toMatchObject({ when: 'daily', time: '05:15' })
    expect(whenOf('0 20 * * 5')).toMatchObject({ when: 'weekly', dow: '5', time: '20:00' })
    expect(whenOf('0 2 15 * *')).toMatchObject({ when: 'monthly', dom: '15' })
    expect(whenOf('45 * * * *')).toMatchObject({ when: 'hourly' })
    expect(whenOf('*/5 * * * *').when).toBe('custom')
    expect(cronOf('weekly', '20:00', '5', '1', '')).toBe('0 20 * * 5')
    expect(cronOf('custom', '00:00', '1', '1', ' 0 4 * * * ')).toBe('0 4 * * *')
  })
  it('warns five minutes before, the same day', () => {
    expect(fiveMinutesBefore('0 5 * * *')).toBe('55 4 * * *')
    expect(fiveMinutesBefore('3 0 * * *')).toBeNull()
    expect(fiveMinutesBefore('*/5 * * * *')).toBeNull()
  })
})
