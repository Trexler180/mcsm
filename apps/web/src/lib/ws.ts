import { api } from './api'

export type WsMessage = {
  type: string
  data: unknown
}

export type ConsoleMessage = {
  type: 'line'
  data: { line: string; stream: 'stdout' | 'stderr'; ts: number; seq?: number }
} | {
  type: 'status'
  data: { status: string }
} | {
  // A run's stream begins. resumed=false means start over: the run's history
  // follows, so clear what is shown first. ServerConsole turns that into a
  // 'reset' message.
  type: 'hello'
  data: { run_id: string; resumed: boolean; status: string }
} | {
  type: 'reset'
  data: null
} | {
  // The socket itself opened or dropped, so a view can say its output is
  // paused rather than look quiet.
  type: 'socket'
  data: { open: boolean }
}

type Listener = (msg: WsMessage) => void

export class ServerConsole {
  private ws: WebSocket | null = null
  private serverId: string
  private listeners = new Set<Listener>()
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private reconnectDelay = 1000
  private closed = false
  // Where this console is in the server's current run, so a reconnect asks
  // only for what it missed rather than replaying the history again.
  private runId = ''
  private lastSeq = 0

  constructor(serverId: string) {
    this.serverId = serverId
  }

  async connect() {
    if (this.closed) return
    // Browsers can't set Authorization on a WebSocket handshake, so mint a
    // single-use ticket and pass that in the query string. A fresh ticket is
    // fetched on every (re)connect, so single-use on the server is fine.
    const consolePath = `/api/v1/servers/${this.serverId}/console`
    const ticket = (await api.auth.ticket(consolePath).then((t) => t.ticket).catch(() => null))
    if (this.closed) return
    if (!ticket) {
      // The panel didn't answer: try again later rather than staying down.
      this.reconnectTimer = setTimeout(() => {
        this.reconnectDelay = Math.min(this.reconnectDelay * 2, 10000)
        this.connect()
      }, this.reconnectDelay)
      return
    }
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const host = window.location.host
    const resume = this.runId && this.lastSeq > 0
      ? `&run=${encodeURIComponent(this.runId)}&since=${this.lastSeq}`
      : ''
    const url = `${protocol}//${host}${consolePath}?ticket=${encodeURIComponent(ticket)}${resume}`

    this.ws = new WebSocket(url)

    this.ws.onopen = () => {
      this.reconnectDelay = 1000
      this.listeners.forEach((l) => l({ type: 'socket', data: { open: true } }))
    }

    this.ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data) as WsMessage
        if (!this.track(msg)) return
        this.listeners.forEach((l) => l(msg))
      } catch {}
    }

    this.ws.onclose = () => {
      this.listeners.forEach((l) => l({ type: 'socket', data: { open: false } }))
      if (!this.closed) {
        this.reconnectTimer = setTimeout(() => {
          this.reconnectDelay = Math.min(this.reconnectDelay * 2, 10000)
          this.connect()
        }, this.reconnectDelay)
      }
    }

    this.ws.onerror = () => {
      this.ws?.close()
    }
  }

  // track follows the run and line position, and reports whether a message
  // should reach listeners (a line already shown should not).
  private track(msg: WsMessage): boolean {
    if (msg.type === 'hello') {
      const d = msg.data as { run_id: string; resumed: boolean }
      if (!d.resumed) {
        this.lastSeq = 0
        this.listeners.forEach((l) => l({ type: 'reset', data: null }))
      }
      this.runId = d.run_id
      return true
    }
    if (msg.type === 'line') {
      const seq = (msg.data as { seq?: number }).seq
      if (seq !== undefined) {
        if (seq <= this.lastSeq) return false
        this.lastSeq = seq
      }
    }
    return true
  }

  send(command: string) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'input', data: { command } }))
    }
  }

  on(listener: Listener) {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  disconnect() {
    this.closed = true
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    this.ws?.close()
    this.ws = null
  }

  get readyState() {
    return this.ws?.readyState ?? WebSocket.CLOSED
  }
}

export class ServerMetrics {
  private ws: WebSocket | null = null
  private serverId: string
  private listeners = new Set<Listener>()
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private closed = false

  constructor(serverId: string) {
    this.serverId = serverId
  }

  async connect() {
    if (this.closed) return
    // See ServerConsole.connect: ticket-based auth for the header-less handshake.
    const metricsPath = `/api/v1/servers/${this.serverId}/metrics`
    const ticket = (await api.auth.ticket(metricsPath).then((t) => t.ticket).catch(() => null))
    if (this.closed || !ticket) return
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${protocol}//${window.location.host}${metricsPath}?ticket=${encodeURIComponent(ticket)}`
    this.ws = new WebSocket(url)

    this.ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data) as WsMessage
        this.listeners.forEach((l) => l(msg))
      } catch {}
    }

    this.ws.onclose = () => {
      if (!this.closed) {
        this.reconnectTimer = setTimeout(() => this.connect(), 3000)
      }
    }
    this.ws.onerror = () => this.ws?.close()
  }

  on(listener: Listener) {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  disconnect() {
    this.closed = true
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    this.ws?.close()
  }
}

type MetricsStreamEntry = {
  stream: ServerMetrics
  subscribers: number
}

// A server page renders live metrics in both its header and dashboard. Share
// one authenticated WebSocket between those consumers so each open page uses
// one ticket, one proxy connection, and one metrics frame instead of two.
const metricsStreams = new Map<string, MetricsStreamEntry>()

export function subscribeServerMetrics(serverId: string, listener: Listener) {
  let entry = metricsStreams.get(serverId)
  if (!entry) {
    entry = { stream: new ServerMetrics(serverId), subscribers: 0 }
    metricsStreams.set(serverId, entry)
    void entry.stream.connect()
  }

  entry.subscribers++
  const unsubscribeListener = entry.stream.on(listener)
  let subscribed = true

  return () => {
    if (!subscribed) return
    subscribed = false
    unsubscribeListener()
    entry!.subscribers--
    if (entry!.subscribers === 0) {
      entry!.stream.disconnect()
      metricsStreams.delete(serverId)
    }
  }
}
