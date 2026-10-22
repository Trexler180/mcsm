import { describe, expect, it, vi } from 'vitest'
import { ApiError, SESSION_ENDED } from '@/lib/api'
import type { Connection } from './connection'
import { DOWN_AFTER, LiveEvents, RETRY_MS, type LiveDeps } from './events'
import type { LiveEvent } from './apply'

class FakeSocket {
  onopen: ((ev: Event) => void) | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  onclose: ((ev: CloseEvent) => void) | null = null
  onerror: ((ev: Event) => void) | null = null
  closed = false
  constructor(public url: string) {}
  close() {
    if (this.closed) return
    this.closed = true
    this.onclose?.({} as CloseEvent)
  }
  send(msg: unknown) {
    this.onmessage?.({ data: JSON.stringify(msg) } as MessageEvent)
  }
}

function harness(ticket: () => Promise<string> = async () => 't') {
  let now = 1_000_000
  const timers: { fn: () => void; at: number }[] = []
  const sockets: FakeSocket[] = []
  const states: Connection[] = []
  const deps: LiveDeps = {
    ticket,
    open: (url) => {
      const s = new FakeSocket(url)
      sockets.push(s)
      return s
    },
    now: () => now,
    setTimer: (fn, ms) => {
      const t = { fn, at: now + ms }
      timers.push(t)
      return t
    },
    clearTimer: (t) => {
      const i = timers.indexOf(t as (typeof timers)[number])
      if (i >= 0) timers.splice(i, 1)
    },
    publish: (c) => states.push(c),
  }
  const events: LiveEvent[] = []
  const handlers = { onEvent: (e: LiveEvent) => events.push(e), onReset: vi.fn(), onBack: vi.fn() }
  const live = new LiveEvents(deps, handlers)
  const flush = () => new Promise((r) => setTimeout(r, 0))
  /** Moves the clock on and fires what's due. */
  const advance = async (ms: number) => {
    now += ms
    for (const t of timers.filter((x) => x.at <= now)) {
      timers.splice(timers.indexOf(t), 1)
      t.fn()
    }
    await flush()
  }
  return { live, sockets, states, events, handlers, flush, advance, last: () => states[states.length - 1] }
}

describe('LiveEvents', () => {
  // A deleted account or a signed-out session fails every ticket the same way;
  // retrying every 15 s behind the sign-in prompt only fills the log.
  it('waits for a sign-in instead of retrying when the session is over', async () => {
    let ended = true
    const h = harness(async () => {
      if (ended) throw new ApiError(401, 'Your session ended.', { code: SESSION_ENDED })
      return 't'
    })
    h.live.start(0)
    await h.flush()
    await h.advance(60_000)
    expect(h.sockets).toHaveLength(0)
    expect(h.last().nextAt).toBeNull()

    ended = false
    h.live.tryNow() // what signing in again does
    await h.flush()
    expect(h.sockets).toHaveLength(1)
  })

  it('connects with a ticket and resumes from the sequence it was given', async () => {
    const h = harness()
    h.live.start(42)
    await h.flush()
    expect(h.sockets).toHaveLength(1)
    const url = new URL(h.sockets[0].url)
    expect(url.pathname).toBe('/api/v1/events')
    expect(url.searchParams.get('ticket')).toBe('t')
    expect(url.searchParams.get('since')).toBe('42')
  })

  it('delivers events once, in order, and remembers where it got to', async () => {
    const h = harness()
    h.live.start(0)
    await h.flush()
    const s = h.sockets[0]
    s.send({ type: 'stream.hello', seq: 10, resumed: true })
    s.send({ seq: 11, type: 'server.status', at: '', server_id: 'a', data: { status: 'online' } })
    s.send({ seq: 11, type: 'server.status', at: '', server_id: 'a', data: { status: 'online' } })
    s.send({ seq: 12, type: 'node.status', at: '', node_id: 'n', data: { online: true } })
    expect(h.events.map((e) => e.seq)).toEqual([11, 12])
    expect(h.live.seq).toBe(12)
  })

  it('pauses, backs off, and resumes from the last event after a drop', async () => {
    const h = harness()
    h.live.start(0)
    await h.flush()
    h.sockets[0].send({ type: 'stream.hello', seq: 5, resumed: true })
    h.sockets[0].send({ seq: 6, type: 'server.stats', at: '', server_id: 'a' })
    h.sockets[0].close()
    expect(h.last().state).toBe('reconnecting')
    expect(h.last().since).not.toBeNull()

    await h.advance(RETRY_MS[0])
    expect(h.sockets).toHaveLength(2)
    expect(new URL(h.sockets[1].url).searchParams.get('since')).toBe('6')
    h.sockets[1].send({ type: 'stream.hello', seq: 9, resumed: true })
    expect(h.last().state).toBe('up')
    expect(h.handlers.onBack).toHaveBeenCalledTimes(1)
    expect(h.handlers.onReset).not.toHaveBeenCalled()
  })

  it('refetches everything when the stream could not resume', async () => {
    const h = harness()
    h.live.start(3)
    await h.flush()
    h.sockets[0].send({ type: 'stream.reset', seq: 900 })
    expect(h.handlers.onReset).toHaveBeenCalledTimes(1)
    expect(h.live.seq).toBe(900)
  })

  it('starts its numbering again when the API restarted', async () => {
    const h = harness()
    h.live.start(0)
    await h.flush()
    h.sockets[0].send({ type: 'stream.hello', seq: 50, resumed: true })
    h.sockets[0].close()
    await h.advance(RETRY_MS[0])
    // The new process's bus is at 2, below what this client saw.
    h.sockets[1].send({ type: 'stream.hello', seq: 2, resumed: true })
    expect(h.handlers.onReset).toHaveBeenCalledTimes(1)
    h.sockets[1].send({ seq: 3, type: 'server.status', at: '', server_id: 'a', data: { status: 'online' } })
    expect(h.events.map((e) => e.seq)).toEqual([3])
  })

  it('calls the panel down only after several tries find nothing answering', async () => {
    let fail = false
    const h = harness(async () => {
      if (fail) throw new TypeError('Failed to fetch')
      return 't'
    })
    h.live.start(0)
    await h.flush()
    h.sockets[0].send({ type: 'stream.hello', seq: 1, resumed: true })
    fail = true
    h.sockets[0].close()
    for (let i = 0; i < DOWN_AFTER - 1; i++) {
      await h.advance(RETRY_MS[RETRY_MS.length - 1])
      expect(h.last().state).toBe('reconnecting')
    }
    await h.advance(RETRY_MS[RETRY_MS.length - 1])
    expect(h.last().state).toBe('down')
    expect(h.last().attempt).toBe(DOWN_AFTER)

    fail = false
    await h.advance(RETRY_MS[RETRY_MS.length - 1])
    h.sockets[h.sockets.length - 1].send({ type: 'stream.hello', seq: 1, resumed: true })
    expect(h.last().state).toBe('up')
    expect(h.handlers.onBack).toHaveBeenCalledTimes(1)
  })

  it('does not call the panel down when it answers and refuses', async () => {
    let fail = false
    const h = harness(async () => {
      if (fail) throw new ApiError(401, 'unauthorized')
      return 't'
    })
    h.live.start(0)
    await h.flush()
    h.sockets[0].send({ type: 'stream.hello', seq: 1, resumed: true })
    fail = true
    h.sockets[0].close()
    for (let i = 0; i < DOWN_AFTER + 2; i++) await h.advance(RETRY_MS[RETRY_MS.length - 1])
    expect(h.last().state).toBe('reconnecting')
  })

  it('tries straight away when asked', async () => {
    const h = harness()
    h.live.start(0)
    await h.flush()
    h.sockets[0].send({ type: 'stream.hello', seq: 1, resumed: true })
    h.sockets[0].close()
    h.live.tryNow()
    await h.flush()
    expect(h.sockets).toHaveLength(2)
  })

  it('goes quiet when stopped', async () => {
    const h = harness()
    h.live.start(0)
    await h.flush()
    h.live.stop()
    expect(h.sockets[0].closed).toBe(true)
    expect(h.last().state).toBe('up')
    await h.advance(60_000)
    expect(h.sockets).toHaveLength(1)
  })
})
