import { api } from './api'

export type WsMessage = {
  type: string
  data: unknown
}

export type ConsoleMessage = {
  type: 'line'
  data: { line: string; stream: 'stdout' | 'stderr'; ts: number }
} | {
  type: 'status'
  data: { status: string }
}

type Listener = (msg: WsMessage) => void

export class ServerConsole {
  private ws: WebSocket | null = null
  private serverId: string
  private listeners = new Set<Listener>()
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private reconnectDelay = 1000
  private closed = false

  constructor(serverId: string) {
    this.serverId = serverId
  }

  async connect() {
    if (this.closed) return
    // Browsers can't set Authorization on a WebSocket handshake, so mint a
    // single-use ticket and pass that in the query string. A fresh ticket is
    // fetched on every (re)connect, so single-use on the server is fine.
    const ticket = (await api.auth.ticket().then((t) => t.ticket).catch(() => null))
    if (this.closed || !ticket) return
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const host = window.location.host
    const url = `${protocol}//${host}/api/v1/servers/${this.serverId}/console?ticket=${encodeURIComponent(ticket)}`

    this.ws = new WebSocket(url)

    this.ws.onopen = () => {
      this.reconnectDelay = 1000
    }

    this.ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data) as WsMessage
        this.listeners.forEach((l) => l(msg))
      } catch {}
    }

    this.ws.onclose = () => {
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
    const ticket = (await api.auth.ticket().then((t) => t.ticket).catch(() => null))
    if (this.closed || !ticket) return
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${protocol}//${window.location.host}/api/v1/servers/${this.serverId}/metrics?ticket=${encodeURIComponent(ticket)}`
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
