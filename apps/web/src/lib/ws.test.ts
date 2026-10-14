import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('./api', () => ({
  api: {
    auth: {
      ticket: vi.fn(async () => ({ ticket: 'test-ticket' })),
    },
  },
}))

import { api } from './api'
import { ServerConsole, subscribeServerMetrics, type WsMessage } from './ws'

class FakeWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  static instances: FakeWebSocket[] = []

  readonly url: string
  readyState = FakeWebSocket.CONNECTING
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  close() {
    this.closed = true
    this.readyState = FakeWebSocket.CLOSED
    this.onclose?.()
  }
}

describe('subscribeServerMetrics', () => {
  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.mocked(api.auth.ticket).mockClear()
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('shares one socket until the last subscriber leaves', async () => {
    const stopHeader = subscribeServerMetrics('srv-1', () => {})
    const stopDashboard = subscribeServerMetrics('srv-1', () => {})

    await vi.waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1))
    expect(api.auth.ticket).toHaveBeenCalledTimes(1)

    stopHeader()
    expect(FakeWebSocket.instances[0].closed).toBe(false)

    stopDashboard()
    expect(FakeWebSocket.instances[0].closed).toBe(true)
  })
})

describe('ServerConsole', () => {
  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  const deliver = (ws: FakeWebSocket, msg: unknown) =>
    ws.onmessage?.({ data: JSON.stringify(msg) } as MessageEvent)

  it('resumes where it left off and never shows a line twice', async () => {
    const sc = new ServerConsole('srv-1')
    const seen: WsMessage[] = []
    sc.on((m) => seen.push(m))
    await sc.connect()
    const first = FakeWebSocket.instances[0]
    expect(first.url).not.toContain('since=')

    deliver(first, { type: 'hello', data: { run_id: 'r1', resumed: false, status: 'online' } })
    deliver(first, { type: 'line', data: { line: 'a', seq: 1 } })
    deliver(first, { type: 'line', data: { line: 'b', seq: 2 } })
    expect(seen.map((m) => m.type)).toEqual(['reset', 'hello', 'line', 'line'])

    // Dropped connection: the reconnect asks for what came after line 2.
    first.close()
    await vi.advanceTimersByTimeAsync(1500)
    const second = FakeWebSocket.instances[1]
    expect(second.url).toContain('run=r1')
    expect(second.url).toContain('since=2')

    seen.length = 0
    deliver(second, { type: 'hello', data: { run_id: 'r1', resumed: true, status: 'online' } })
    deliver(second, { type: 'line', data: { line: 'b', seq: 2 } }) // already shown
    deliver(second, { type: 'line', data: { line: 'c', seq: 3 } })
    expect(seen.map((m) => m.type)).toEqual(['hello', 'line'])
    expect((seen[1].data as { line: string }).line).toBe('c')

    // A new run starts over.
    seen.length = 0
    deliver(second, { type: 'hello', data: { run_id: 'r2', resumed: false, status: 'starting' } })
    deliver(second, { type: 'line', data: { line: 'fresh', seq: 1 } })
    expect(seen.map((m) => m.type)).toEqual(['reset', 'hello', 'line'])
    sc.disconnect()
  })
})
