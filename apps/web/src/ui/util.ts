// Small helpers the design system and pages share.

export const SEC = 1000
export const MIN = 60 * SEC
export const HOUR = 60 * MIN
export const DAY = 24 * HOUR

/** Joins the truthy class names. */
export function cx(...parts: Array<string | false | null | undefined | 0>): string {
  return parts.filter(Boolean).join(' ')
}

export const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v))
export const pad2 = (n: number) => String(n).padStart(2, '0')
export const cap = (s: string) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s)

/** "1 server", "3 servers"; pl overrides the plural ("people"). */
export function plural(n: number, word: string, pl?: string): string {
  return `${n} ${n === 1 ? word : pl || word + 's'}`
}

/** FNV-1a, for stable colors and seeds derived from a name. */
export function hashStr(s: string): number {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}

/** A seeded random source (mulberry32), so drawings derived from a name are
 *  the same on every render. */
export function rng(seed: string | number): () => number {
  let a = typeof seed === 'string' ? hashStr(seed) : seed >>> 0
  return () => {
    a |= 0
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export function pick<T>(r: () => number, arr: readonly T[]): T {
  return arr[Math.floor(r() * arr.length)]
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

/** Milliseconds since the epoch for an API timestamp, or null. */
export function toMs(t: string | number | null | undefined): number | null {
  if (t == null || t === '') return null
  if (typeof t === 'number') return t
  const ms = Date.parse(t)
  return Number.isNaN(ms) ? null : ms
}
