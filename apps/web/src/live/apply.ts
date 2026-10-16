import type { FleetNode, FleetServer, FleetSummary, Node, Operation, Server, ServerStatus } from '@/lib/types'

// Applying a live event to what's on screen. Pure functions over the cached
// API responses, so the same event lands the same way on Home, the sidebar
// and the older screens. Every change is idempotent: an event that arrives
// twice (a resumed stream replaying what a fresh fetch already had) is
// harmless.

/** One event from /api/v1/events. */
export interface LiveEvent {
  seq: number
  type: string
  at: string
  server_id?: string
  node_id?: string
  data?: Record<string, unknown>
}

const num = (v: unknown): number | null => (typeof v === 'number' && Number.isFinite(v) ? v : null)

function onlineSince(e: LiveEvent): number | null | undefined {
  const status = e.data?.status
  if (status !== 'online') return null
  const started = e.data?.started_at
  if (typeof started !== 'string') return undefined
  const ms = Date.parse(started)
  return Number.isNaN(ms) ? undefined : Math.floor(ms / 1000)
}

function patchServer(s: FleetServer, e: LiveEvent): FleetServer {
  switch (e.type) {
    case 'server.status': {
      const status = e.data?.status as ServerStatus | undefined
      if (!status) return s
      const since = onlineSince(e)
      const next: FleetServer = { ...s, status }
      if (since !== undefined) next.online_since = since
      // Live numbers only describe a running server.
      if (status !== 'online') {
        next.players = null
        next.tps = null
        next.cpu_percent = null
        next.ram_used_mb = null
      }
      return next
    }
    case 'server.stats':
      if (s.status !== 'online') return s
      return {
        ...s,
        players: num(e.data?.players) ?? s.players,
        tps: num(e.data?.tps) ?? s.tps,
        cpu_percent: num(e.data?.cpu_percent) ?? s.cpu_percent,
        ram_used_mb: num(e.data?.ram_used_mb) ?? s.ram_used_mb,
        sampled_at: e.at,
      }
    case 'server.players': {
      const online = num(e.data?.online)
      return online == null || s.status !== 'online' ? s : { ...s, players: online }
    }
    case 'operation.updated': {
      const op = e.data as unknown as Operation | undefined
      if (!op?.id) return s
      if (op.status === 'running') return { ...s, operation: op }
      return s.operation?.id === op.id ? { ...s, operation: null } : s
    }
    default:
      return s
  }
}

function patchNode(n: FleetNode, e: LiveEvent): FleetNode {
  if (e.type !== 'node.status') return n
  const d = e.data ?? {}
  return {
    ...n,
    online: d.online === true,
    cpu_pct: num(d.cpu_pct) ?? n.cpu_pct,
    mem_used_mb: num(d.mem_used_mb) ?? n.mem_used_mb,
    memory_mb: num(d.memory_mb) ?? n.memory_mb,
    disk_used_gb: num(d.disk_used_gb) ?? n.disk_used_gb,
    disk_gb: num(d.disk_gb) ?? n.disk_gb,
    last_seen: d.online === true ? e.at : n.last_seen,
  }
}

/** Recounts the figures that follow from the servers and nodes. */
export function refigure(f: FleetSummary): FleetSummary {
  let players = 0
  let up = 0
  let unknown = 0
  for (const s of f.servers) {
    if (s.status === 'online') {
      up++
      players += s.players ?? 0
    } else if (s.status === 'unknown') unknown++
  }
  return {
    ...f,
    figures: {
      ...f.figures,
      players_online: players,
      servers_up: up,
      servers_unknown: unknown,
      servers_total: f.servers.length,
      nodes_online: f.nodes.filter((n) => n.online).length,
      nodes_total: f.nodes.length,
    },
  }
}

export function applyToFleet(f: FleetSummary, e: LiveEvent): FleetSummary {
  if (e.type === 'server.deleted' && e.server_id) {
    return refigure({ ...f, servers: f.servers.filter((s) => s.id !== e.server_id) })
  }
  if (e.server_id) {
    let changed = false
    const servers = f.servers.map((s) => {
      if (s.id !== e.server_id) return s
      const next = patchServer(s, e)
      if (next !== s) changed = true
      return next
    })
    return changed ? refigure({ ...f, servers }) : f
  }
  if (e.node_id) {
    let changed = false
    const nodes = f.nodes.map((n) => {
      if (n.id !== e.node_id) return n
      const next = patchNode(n, e)
      if (next !== n) changed = true
      return next
    })
    return changed ? refigure({ ...f, nodes }) : f
  }
  return f
}

export function applyToServer(s: Server, e: LiveEvent): Server {
  if (e.type !== 'server.status' || e.server_id !== s.id) return s
  const status = e.data?.status as ServerStatus | undefined
  if (!status || status === s.status) return s
  const since = onlineSince(e)
  const next: Server = { ...s, status }
  if (since === null) delete next.online_since
  else if (since !== undefined) next.online_since = since
  return next
}

export function applyToNode(n: Node, e: LiveEvent): Node {
  if (e.type !== 'node.status' || e.node_id !== n.id) return n
  const d = e.data ?? {}
  return {
    ...n,
    online: d.online === true,
    cpu_pct: num(d.cpu_pct) ?? n.cpu_pct,
    mem_used_mb: num(d.mem_used_mb) ?? n.mem_used_mb,
    memory_mb: num(d.memory_mb) ?? n.memory_mb,
    disk_used_gb: num(d.disk_used_gb) ?? n.disk_used_gb,
    disk_gb: num(d.disk_gb) ?? n.disk_gb,
    last_seen: d.online === true ? e.at : n.last_seen,
  }
}
