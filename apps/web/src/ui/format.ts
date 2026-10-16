import { DAY, HOUR, MIN, SEC, pad2 } from './util'

// How the panel writes sizes, durations and times. One set of rules, so the
// same quantity reads the same on every screen.

export const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
export const DOW = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
export const DOW_FULL = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']

export function fmtMB(mb: number | null | undefined, digits?: number): string {
  if (mb == null) return '—'
  if (mb >= 1024) {
    const g = mb / 1024
    const d = digits != null ? digits : Number.isInteger(g) ? 0 : 1
    return g.toFixed(d) + ' GB'
  }
  return Math.round(mb) + ' MB'
}

export function fmtBytes(b: number | null | undefined): string {
  if (b == null) return '—'
  if (b < 1024) return b + ' B'
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = b / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return (v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2)) + ' ' + units[i]
}

/** "2d 4h", "3m", "12s": the largest `parts` units. */
export function fmtDur(ms: number | null | undefined, parts = 2): string {
  if (ms == null || ms < 0) return '—'
  const s = Math.floor(ms / 1000)
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  const out: string[] = []
  if (d) out.push(d + 'd')
  if (h) out.push(h + 'h')
  if (m && out.length < parts) out.push(m + 'm')
  if (!out.length) out.push(sec + 's')
  return out.slice(0, parts).join(' ')
}

export function fmtClock(ts: number, secs = false): string {
  const d = new Date(ts)
  return pad2(d.getHours()) + ':' + pad2(d.getMinutes()) + (secs ? ':' + pad2(d.getSeconds()) : '')
}

/** "Mar 4, 14:03", with the year when it isn't this one ("Nov 5 2024"). */
export function fmtDate(ts: number, withTime = true, now = Date.now()): string {
  const d = new Date(ts)
  let s = MONTHS[d.getMonth()] + ' ' + d.getDate()
  if (d.getFullYear() !== new Date(now).getFullYear()) s += ' ' + d.getFullYear()
  return withTime ? s + ', ' + fmtClock(ts) : s
}

/** "just now", "5m ago", "in 2h", or the date once it's a month away. */
export function relTime(ts: number | null | undefined, now = Date.now()): string {
  if (!ts) return 'never'
  const d = now - ts
  const fut = d < 0
  const a = Math.abs(d)
  let s: string
  if (a < 45 * SEC) return fut ? 'in a moment' : 'just now'
  if (a < HOUR) s = Math.round(a / MIN) + 'm'
  else if (a < DAY) s = Math.round(a / HOUR) + 'h'
  else if (a < 30 * DAY) s = Math.round(a / DAY) + 'd'
  else return fmtDate(ts, false)
  return fut ? 'in ' + s : s + ' ago'
}

export function fmtDay(ts: number, now = Date.now()): string {
  const d = new Date(ts)
  const today = new Date(now)
  today.setHours(0, 0, 0, 0)
  const t = new Date(ts)
  t.setHours(0, 0, 0, 0)
  const diff = Math.round((today.getTime() - t.getTime()) / DAY)
  if (diff === 0) return 'Today'
  if (diff === 1) return 'Yesterday'
  if (diff === -1) return 'Tomorrow'
  return DOW[d.getDay()] + ', ' + MONTHS[d.getMonth()] + ' ' + d.getDate()
}

export function fmtNum(n: number | null | undefined): string {
  return n == null ? '—' : Math.round(n).toLocaleString('en-US')
}

export function fmtCompact(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(n >= 1e7 ? 0 : 1) + 'M'
  if (n >= 1e3) return (n / 1e3).toFixed(n >= 1e4 ? 0 : 1) + 'K'
  return String(n)
}
