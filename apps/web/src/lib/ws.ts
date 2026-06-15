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
    const token = (await api.auth.ensureAccessToken().catch(() => null)) ?? ''
    if (this.closed) return
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const host = window.location.host
    // Browsers can't set Authorization on a WebSocket handshake — pass the JWT
    // in the query string instead. The API middleware accepts both.
    const url = `${protocol}//${host}/api/v1/servers/${this.serverId}/console?token=${encodeURIComponent(token)}`

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
    const token = (await api.auth.ensureAccessToken().catch(() => null)) ?? ''
    if (this.closed) return
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${protocol}//${window.location.host}/api/v1/servers/${this.serverId}/metrics?token=${encodeURIComponent(token)}`
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
