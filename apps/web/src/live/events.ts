import { ApiError, api } from '@/lib/api'
import { INITIAL_CONNECTION, useConnection, type Connection } from './connection'
import type { LiveEvent } from './apply'

// The panel's live feed: one WebSocket to /api/v1/events carrying every event
// the signed-in person may see. It resumes where it left off after a drop
// (?since=<seq>), and when the gap is too old to replay the server says
// stream.reset and everything on screen is fetched again, so "caught up" is
// always true when it's said.

const STREAM_PATH = '/api/v1/events'

/** Waits between tries after updates stop: quick at first, then every 15 s. */
export const RETRY_MS = [1000, 2000, 4000, 8000, 15000]

/** Failed tries, with the panel not answering, before it's called down. */
export const DOWN_AFTER = 3

interface SocketLike {
  onopen: ((ev: Event) => void) | null
  onmessage: ((ev: MessageEvent) => void) | null
  onclose: ((ev: CloseEvent) => void) | null
  onerror: ((ev: Event) => void) | null
  close: (code?: number, reason?: string) => void
}

export interface LiveDeps {
  /** A single-use ticket for the stream. */
  ticket: () => Promise<string>
  open: (url: string) => SocketLike
  now: () => number
  setTimer: (fn: () => void, ms: number) => unknown
  clearTimer: (t: unknown) => void
  /** Where the connection state goes (the store, or a test's spy). */
  publish: (c: Connection) => void
}

export interface LiveHandlers {
  /** An event to apply to what's on screen. */
  onEvent: (e: LiveEvent) => void
  /** The stream couldn't resume: fetch everything again. */
  onReset: () => void
  /** Updates are flowing again after a drop of gapMs. */
  onBack: (gapMs: number) => void
}

/** Whether a failure means nothing answered (rather than an answer saying no). */
function unreachable(e: unknown): boolean {
  if (e instanceof ApiError) return e.status === 502 || e.status === 503 || e.status === 504
  return e instanceof TypeError || (e instanceof DOMException && e.name === 'TimeoutError')
}

export class LiveEvents {
  private socket: SocketLike | null = null
  private timer: unknown = null
  private running = false
  private lastSeq = 0
  private conn: Connection = { ...INITIAL_CONNECTION }
  /** Whether the latest failed try found the panel not answering at all. */
  private panelSilent = false
  private generation = 0

  constructor(
    private deps: LiveDeps,
    private handlers: LiveHandlers,
  ) {}

  /** Starts the stream from `since`, the event sequence the screen's data was
   *  fetched at (0 for "from now"). */
  start(since = 0) {
    if (this.running) return
    this.running = true
    this.lastSeq = Math.max(this.lastSeq, since)
    this.set({ ...INITIAL_CONNECTION })
    void this.connect()
  }

  stop() {
    this.running = false
    this.generation++
    this.deps.clearTimer(this.timer)
    this.timer = null
    const s = this.socket
    this.socket = null
    if (s) {
      s.onclose = null
      s.close()
    }
    this.set({ ...INITIAL_CONNECTION })
  }

  /** Tries again now instead of waiting for the next scheduled try. */
  tryNow() {
    if (!this.running || this.conn.state === 'up') return
    this.deps.clearTimer(this.timer)
    this.timer = null
    void this.attempt()
  }

  get seq() {
    return this.lastSeq
  }

  private set(c: Connection) {
    this.conn = c
    this.deps.publish(c)
  }

  private async connect() {
    const gen = this.generation
    let ticket: string
    try {
      ticket = await this.deps.ticket()
    } catch (e) {
      if (gen !== this.generation) return
      this.panelSilent = unreachable(e)
      this.lost()
      return
    }
    if (gen !== this.generation || !this.running) return
    this.panelSilent = false
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const q = new URLSearchParams({ ticket })
    if (this.lastSeq > 0) q.set('since', String(this.lastSeq))
    const socket = this.deps.open(`${proto}//${window.location.host}${STREAM_PATH}?${q}`)
    this.socket = socket
    socket.onmessage = (ev) => {
      if (gen !== this.generation) return
      this.receive(ev.data)
    }
    socket.onclose = () => {
      if (gen !== this.generation || this.socket !== socket) return
      this.socket = null
      this.lost()
    }
    socket.onerror = () => socket.close()
  }

  private receive(raw: unknown) {
    let msg: { type?: string; seq?: number; resumed?: boolean }
    try {
      msg = JSON.parse(String(raw))
    } catch {
      return
    }
    if (msg.type === 'stream.hello' || msg.type === 'stream.reset') {
      // A hello numbered below where this client got to means the API
      // restarted and its numbering began again: that is a reset too.
      const restarted = typeof msg.seq === 'number' && msg.seq < this.lastSeq
      const reset = msg.type === 'stream.reset' || restarted
      if (typeof msg.seq === 'number') {
        // A fresh stream starts from now; a reset continues from its number.
        if (reset || this.lastSeq === 0) this.lastSeq = msg.seq
      }
      if (reset) this.handlers.onReset()
      this.flowing()
      return
    }
    const e = msg as LiveEvent
    if (typeof e.seq === 'number') {
      if (e.seq <= this.lastSeq) return
      this.lastSeq = e.seq
    }
    this.handlers.onEvent(e)
  }

  /** The stream said hello: updates are flowing. */
  private flowing() {
    const was = this.conn
    this.set({ ...INITIAL_CONNECTION })
    if (was.state !== 'up' && was.since != null) this.handlers.onBack(this.deps.now() - was.since)
  }

  /** The stream is gone (or never came up): say so and schedule a try. */
  private lost() {
    if (!this.running) return
    const now = this.deps.now()
    const c = this.conn
    if (c.state === 'up') {
      this.set({ state: 'reconnecting', since: now, attempt: 0, nextAt: null, checkedAt: now })
    }
    this.schedule()
  }

  private schedule() {
    const c = this.conn
    const wait = RETRY_MS[Math.min(c.attempt, RETRY_MS.length - 1)]
    this.set({ ...c, nextAt: this.deps.now() + wait })
    this.deps.clearTimer(this.timer)
    this.timer = this.deps.setTimer(() => {
      this.timer = null
      void this.attempt()
    }, wait)
  }

  private async attempt() {
    if (!this.running) return
    const c = this.conn
    const attempt = c.attempt + 1
    // Counted before the try so the banner can say "Still reconnecting" while
    // it runs; the panel is called down only once tries keep finding nothing.
    this.set({ ...c, attempt, checkedAt: this.deps.now(), nextAt: null })
    const stale = this.socket
    this.socket = null
    if (stale) {
      stale.onclose = null
      stale.close()
    }
    this.generation++
    const gen = this.generation
    await this.connect()
    if (gen !== this.generation) return
    if (this.conn.state !== 'up' && this.panelSilent && attempt >= DOWN_AFTER && this.conn.state !== 'down') {
      this.set({ ...this.conn, state: 'down' })
    }
  }
}

function browserDeps(): LiveDeps {
  return {
    ticket: async () => (await api.auth.ticket(STREAM_PATH)).ticket,
    open: (url) => new WebSocket(url),
    now: () => Date.now(),
    setTimer: (fn, ms) => setTimeout(fn, ms),
    clearTimer: (t) => clearTimeout(t as ReturnType<typeof setTimeout>),
    publish: (c) => useConnection.setState(c),
  }
}

let live: LiveEvents | null = null

/** Starts the panel's live feed. Call once the screen's first data is in. */
export function startLive(handlers: LiveHandlers, since: number) {
  if (!live) {
    live = new LiveEvents(browserDeps(), handlers)
    useConnection.setState({ tryNow: () => live?.tryNow() })
  }
  live.start(since)
}

export function stopLive() {
  live?.stop()
  live = null
  useConnection.setState({ ...INITIAL_CONNECTION, tryNow: () => {} })
}

/** Tries to reconnect now, after the person signs back in, say. */
export function reconnectLive() {
  live?.tryNow()
}
