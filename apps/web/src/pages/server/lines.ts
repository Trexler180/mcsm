// Reading a server's console output: each raw line becomes something the
// console can colour and filter by what it is (an error, a join, chat, a
// command someone typed, a note from the panel itself).

export type LineKind = 'log' | 'sys' | 'cmd' | 'chat' | 'join'
export type LineLevel = 'INFO' | 'WARN' | 'ERROR' | 'FATAL'

export interface ConsoleLine {
  /** Unique within one console view, for keys. */
  id: number
  /** When it arrived (ms). */
  ts: number
  /** The time printed on the line itself, when it has one ("12:04:31"). */
  time?: string
  kind: LineKind
  level: LineLevel
  text: string
  /** A command that never reached the server. */
  failed?: boolean
}

// \x1b[...m colour codes, and the odd bare reset Paper writes on Windows.
// eslint-disable-next-line no-control-regex -- matching escape codes is the point
const ANSI = /\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07/g
// [12:04:31] [Server thread/INFO]: …, with Forge's longer date and its
// [logger/] block allowed: [16Oct2026 12:04:31.123] [main/INFO] [net.minecraft/]: …
const LOG = /^\[(?:[^\]]*?\s)?(\d{2}:\d{2}:\d{2})(?:\.\d+)?\]\s+\[([^\]]+?)\/([A-Za-z]+)\](?:\s+\[[^\]]*\])?:?\s?(.*)$/
// Paper and Spigot on a console: [12:04:31 INFO]: …
const SHORT = /^\[(\d{2}:\d{2}:\d{2})(?:\.\d+)?\s+([A-Za-z]+)\]:?\s?(.*)$/
const CONTINUATION = /^(\s+\S|at\s|Caused by:|\.\.\. \d+ more|Suppressed:)/

function levelOf(raw: string): LineLevel {
  const l = raw.toUpperCase()
  if (l === 'WARN' || l === 'WARNING') return 'WARN'
  if (l === 'ERROR' || l === 'SEVERE') return 'ERROR'
  if (l === 'FATAL') return 'FATAL'
  return 'INFO'
}

export const stripAnsi = (s: string) => s.replace(ANSI, '')

/**
 * Parses one line of output. `prev` is the line before it, so a stack trace's
 * "at …" lines keep the level of the error they belong to.
 */
export function parseLine(raw: string, opts: { id: number; ts: number; stderr?: boolean; prev?: ConsoleLine }): ConsoleLine {
  const clean = stripAnsi(raw).replace(/\r$/, '')
  const base = { id: opts.id, ts: opts.ts }
  if (/^\[mcsm\]\s?/i.test(clean)) {
    const text = clean.replace(/^\[mcsm\]\s?/i, '')
    return { ...base, kind: 'sys', level: /^warning:/i.test(text) ? 'WARN' : 'INFO', text }
  }
  const long = clean.match(LOG)
  const short = long ? null : clean.match(SHORT)
  const m = long ? [long[1], long[3], long[4]] : short ? [short[1], short[2], short[3]] : null
  if (m) {
    const [time, lvl, msg] = m
    const level = levelOf(lvl)
    let kind: LineKind = 'log'
    if (level === 'INFO') {
      if (/^<[^>]+>\s/.test(msg) || /^\[Not Secure\]\s<[^>]+>/.test(msg)) kind = 'chat'
      else if (/^\S+ (joined|left) the game$/.test(msg)) kind = 'join'
    }
    return { ...base, time, kind, level, text: msg }
  }
  if (opts.prev && opts.prev.level !== 'INFO' && opts.prev.kind === 'log' && CONTINUATION.test(clean)) {
    return { ...base, kind: 'log', level: opts.prev.level, text: clean }
  }
  const looksBad = /exception|error|fatal/i.test(clean)
  return { ...base, kind: 'log', level: opts.stderr ? (looksBad ? 'ERROR' : 'WARN') : 'INFO', text: clean }
}

/** Parses a whole log file's lines. */
export function parseLines(raw: string[], ts = Date.now()): ConsoleLine[] {
  const out: ConsoleLine[] = []
  raw.forEach((r, i) => out.push(parseLine(r, { id: i + 1, ts, prev: out[out.length - 1] })))
  return out
}

/** "has made the advancement [Stone Age]": the advancement, to set apart. */
export function splitAdvancement(text: string): [string, string] | null {
  const m = text.match(/^(.*has (?:made the advancement|completed the challenge|reached the goal) )(\[.*\])$/)
  return m ? [m[1], m[2]] : null
}
