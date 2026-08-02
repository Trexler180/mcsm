import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('./api', () => ({
  api: {
    auth: {
      ticket: vi.fn(async () => ({ ticket: 'test-ticket' })),
    },
  },
}))

import { api } from './api'
import { subscribeServerMetrics } from './ws'

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
