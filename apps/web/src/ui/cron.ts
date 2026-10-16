import { DOW_FULL } from './format'
import { DAY, pad2 } from './util'

// Schedules as people read them: five-field cron expressions parsed,
// described in 24-hour time, and stepped forward to their next runs. Times
// are the panel's own clock.

interface Parsed {
  min: Set<number>
  hour: Set<number>
  dom: Set<number>
  mon: Set<number>
  dow: Set<number>
  domStar: boolean
  dowStar: boolean
}

function parseField(f: string, lo: number, hi: number): Set<number> | null {
  const out = new Set<number>()
  for (const part of f.split(',')) {
    const [range, stepS] = part.split('/')
    const step = stepS ? parseInt(stepS, 10) : 1
    if (!step || step < 1) return null
    let a: number
    let b: number
    if (range === '*') {
      a = lo
      b = hi
    } else if (range.includes('-')) {
      ;[a, b] = range.split('-').map((x) => parseInt(x, 10))
    } else {
      a = parseInt(range, 10)
      b = stepS ? hi : a
    }
    if (Number.isNaN(a) || Number.isNaN(b) || a < lo || b > hi || a > b) return null
    for (let i = a; i <= b; i += step) out.add(i)
  }
  return out
}

export function parseCron(expr: string): Parsed | null {
  const p = String(expr || '').trim().split(/\s+/)
  if (p.length !== 5) return null
  const min = parseField(p[0], 0, 59)
  const hour = parseField(p[1], 0, 23)
  const dom = parseField(p[2], 1, 31)
  const mon = parseField(p[3], 1, 12)
  const dow = parseField(p[4].replace(/7/g, '0'), 0, 6)
  if (!min || !hour || !dom || !mon || !dow) return null
  return { min, hour, dom, mon, dow, domStar: p[2] === '*', dowStar: p[4] === '*' }
}

/** The first run after `from`, or null if the expression never runs. */
export function nextRun(expr: string, from = Date.now()): number | null {
  const c = parseCron(expr)
  if (!c) return null
  const d = new Date(from)
  d.setSeconds(0, 0)
  d.setMinutes(d.getMinutes() + 1)
  for (let i = 0; i < 200000; i++) {
    if (!c.mon.has(d.getMonth() + 1)) {
      d.setMonth(d.getMonth() + 1, 1)
      d.setHours(0, 0, 0, 0)
      continue
    }
    const domOk = c.dom.has(d.getDate())
    const dowOk = c.dow.has(d.getDay())
    const dayOk = c.domStar && c.dowStar ? true : c.domStar ? dowOk : c.dowStar ? domOk : domOk || dowOk
    if (!dayOk) {
      d.setDate(d.getDate() + 1)
      d.setHours(0, 0, 0, 0)
      continue
    }
    if (!c.hour.has(d.getHours())) {
      d.setHours(d.getHours() + 1, 0, 0, 0)
      continue
    }
    if (!c.min.has(d.getMinutes())) {
      d.setMinutes(d.getMinutes() + 1, 0, 0)
      continue
    }
    return d.getTime()
  }
  return null
}

/** The next n runs after `from`. */
export function nextRuns(expr: string, n: number, from = Date.now()): number[] {
  const out: number[] = []
  let t: number | null = from
  for (let i = 0; i < n; i++) {
    t = nextRun(expr, t)
    if (t == null) break
    out.push(t)
  }
  return out
}

const ordinal = (n: number) => n + (n % 10 === 1 && n !== 11 ? 'st' : n % 10 === 2 && n !== 12 ? 'nd' : n % 10 === 3 && n !== 13 ? 'rd' : 'th')

/** "Every day at 04:00", "Weekdays at 06:30", "Every 15 minutes". */
export function describeCron(expr: string): string {
  if (!parseCron(expr)) return 'Invalid schedule'
  const [m, h, dom, mon, dow] = String(expr).trim().split(/\s+/)
  const at = /^\d+$/.test(m) && /^\d+$/.test(h) ? pad2(+h) + ':' + pad2(+m) : null
  if (/^\*\/\d+$/.test(m) && h === '*' && dom === '*' && mon === '*' && dow === '*') return `Every ${m.slice(2)} minutes`
  if (m === '*' && h === '*') return 'Every minute'
  if (/^\d+$/.test(m) && h === '*' && dom === '*' && dow === '*') return m === '0' ? 'Every hour, on the hour' : `Every hour at :${pad2(+m)}`
  if (/^\d+$/.test(m) && /^\*\/\d+$/.test(h) && dom === '*' && dow === '*') return `Every ${h.slice(2)} hours at :${pad2(+m)}`
  if (at && dom === '*' && mon === '*' && dow === '*') return `Every day at ${at}`
  if (at && dom === '*' && mon === '*' && dow === '1-5') return `Weekdays at ${at}`
  if (at && dom === '*' && mon === '*' && (dow === '0,6' || dow === '6,0')) return `Weekends at ${at}`
  if (at && dom === '*' && mon === '*' && /^\d$/.test(dow)) return `Every ${DOW_FULL[+dow % 7]} at ${at}`
  if (at && dom === '*' && mon === '*' && /^[\d,]+$/.test(dow))
    return `${dow
      .split(',')
      .map((d) => DOW_FULL[+d % 7].slice(0, 3))
      .join(', ')} at ${at}`
  if (at && /^\d+$/.test(dom) && mon === '*' && dow === '*') return `Monthly on the ${ordinal(+dom)} at ${at}`
  return 'Custom: ' + expr
}

export type When = 'hourly' | 'daily' | 'weekly' | 'monthly' | 'custom'

/** The friendly shape of an expression, for the schedule editor. */
export function whenOf(expr: string): { when: When; time: string; dow: string; dom: string } {
  const p = String(expr || '').trim().split(/\s+/)
  const time = /^\d+$/.test(p[0] ?? '') && /^\d+$/.test(p[1] ?? '') ? pad2(+p[1]) + ':' + pad2(+p[0]) : /^\d+$/.test(p[0] ?? '') ? '00:' + pad2(+p[0]) : '05:00'
  const dow = /^\d$/.test(p[4] ?? '') ? p[4] : '1'
  const dom = /^\d+$/.test(p[2] ?? '') ? p[2] : '1'
  if (p.length !== 5 || p[3] !== '*') return { when: 'custom', time, dow, dom }
  if (p[1] === '*' && /^\d+$/.test(p[0]) && p[2] === '*' && p[4] === '*') return { when: 'hourly', time, dow, dom }
  if (!/^\d+$/.test(p[0]) || !/^\d+$/.test(p[1])) return { when: 'custom', time, dow, dom }
  if (p[2] === '*' && p[4] === '*') return { when: 'daily', time, dow, dom }
  if (p[2] === '*' && /^\d$/.test(p[4])) return { when: 'weekly', time, dow, dom }
  if (/^\d+$/.test(p[2]) && p[4] === '*') return { when: 'monthly', time, dow, dom }
  return { when: 'custom', time, dow, dom }
}

/** The expression for a friendly shape. */
export function cronOf(when: When, time: string, dow: string, dom: string, custom: string): string {
  const [hh, mm] = time.split(':').map((x) => +x)
  switch (when) {
    case 'hourly':
      return `${mm} * * * *`
    case 'daily':
      return `${mm} ${hh} * * *`
    case 'weekly':
      return `${mm} ${hh} * * ${dow}`
    case 'monthly':
      return `${mm} ${hh} ${dom} * *`
    default:
      return custom.trim()
  }
}

/** Five minutes before a fixed-time expression, for a warning in chat. */
export function fiveMinutesBefore(expr: string): string | null {
  const [m, h, ...rest] = expr.split(' ')
  if (!/^\d+$/.test(m) || !/^\d+$/.test(h) || rest.length !== 3) return null
  // Crossing midnight would move the day too; only same-day warnings.
  let wm = +m - 5
  let wh = +h
  if (wm < 0) {
    wm += 60
    wh -= 1
  }
  if (wh < 0) return null
  return [wm, wh, ...rest].join(' ')
}

/** A signed, coarse offset from now: "now", "−2h", "+3d". */
export function relLabel(t: number, now = Date.now()): string {
  const d = t - now
  const a = Math.abs(d)
  if (a < 60_000) return 'now'
  const v = a < 3600_000 ? Math.round(a / 60_000) + 'm' : a < DAY ? Math.round(a / 3600_000) + 'h' : Math.round(a / DAY) + 'd'
  return (d < 0 ? '−' : '+') + v
}
