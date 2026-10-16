import { describe, expect, it } from 'vitest'
import type { FleetServer, FleetSummary, Operation, Server } from '@/lib/types'
import { applyToFleet, applyToServer, type LiveEvent } from './apply'

function server(id: string, over: Partial<FleetServer> = {}): FleetServer {
  return {
    id,
    name: id,
    status: 'online',
    platform: 'paper',
    mc_version: '1.21.4',
    node_id: 'n1',
    folder_id: null,
    sort_order: 0,
    port: 25565,
    online_since: 100,
    players: 3,
    tps: 20,
    cpu_percent: 10,
    ram_used_mb: 2048,
    ram_limit_mb: 4096,
    sampled_at: null,
    players_24h: [],
    last_backup_at: null,
    backup_failed_at: null,
    backup_overdue: false,
    active_conflict: false,
    mod_updates: null,
    operation: null,
    ...over,
  }
}

function fleet(servers: FleetServer[]): FleetSummary {
  return {
    generated_at: '',
    figures: {
      players_online: 0,
      servers_up: 0,
      servers_total: 0,
      servers_unknown: 0,
      nodes_online: 0,
      nodes_total: 0,
      alerts_unread: 2,
      backups_overdue: 0,
      mod_updates: 0,
    },
    servers,
    nodes: [{ id: 'n1', name: 'n1', online: true, cpu_pct: 5, mem_used_mb: 1, memory_mb: 2, disk_used_gb: 1, disk_gb: 2, last_seen: null }],
    upcoming: [],
    activity: [],
    event_seq: 1,
  }
}

const ev = (e: Partial<LiveEvent>): LiveEvent => ({ seq: 1, type: '', at: '2026-10-16T10:00:00Z', ...e })

describe('applyToFleet', () => {
  it('stops a server and clears its live numbers', () => {
    const f = applyToFleet(fleet([server('a'), server('b', { players: 2 })]), ev({ type: 'server.status', server_id: 'a', data: { status: 'offline' } }))
    const a = f.servers[0]
    expect(a.status).toBe('offline')
    expect(a.players).toBeNull()
    expect(a.online_since).toBeNull()
    expect(f.figures.servers_up).toBe(1)
    expect(f.figures.players_online).toBe(2)
    expect(f.figures.alerts_unread).toBe(2)
  })

  it('takes the start time from a server coming up', () => {
    const f = applyToFleet(fleet([server('a', { status: 'starting', online_since: null })]), ev({
      type: 'server.status',
      server_id: 'a',
      data: { status: 'online', started_at: '2026-10-16T09:00:00Z' },
    }))
    expect(f.servers[0].online_since).toBe(Date.parse('2026-10-16T09:00:00Z') / 1000)
  })

  it('applies stats only to a running server', () => {
    const stats = ev({ type: 'server.stats', server_id: 'a', data: { players: 7, tps: 19.5, cpu_percent: 40, ram_used_mb: 3000 } })
    const up = applyToFleet(fleet([server('a')]), stats)
    expect(up.servers[0]).toMatchObject({ players: 7, tps: 19.5, cpu_percent: 40, ram_used_mb: 3000, sampled_at: stats.at })
    expect(up.figures.players_online).toBe(7)
    const down = fleet([server('a', { status: 'offline', players: null })])
    expect(applyToFleet(down, stats)).toBe(down)
  })

  it('tracks the running operation and lets it go when it ends', () => {
    const op = { id: 'op1', server_id: 'a', kind: 'backup', status: 'running' } as Operation
    const started = applyToFleet(fleet([server('a')]), ev({ type: 'operation.updated', server_id: 'a', data: op as never }))
    expect(started.servers[0].operation?.id).toBe('op1')
    const ended = applyToFleet(started, ev({ type: 'operation.updated', server_id: 'a', data: { ...op, status: 'succeeded' } as never }))
    expect(ended.servers[0].operation).toBeNull()
  })

  it('follows a node going offline', () => {
    const f = applyToFleet(fleet([server('a')]), ev({ type: 'node.status', node_id: 'n1', data: { online: false } }))
    expect(f.nodes[0].online).toBe(false)
    expect(f.figures.nodes_online).toBe(0)
  })

  it('removes a deleted server', () => {
    const f = applyToFleet(fleet([server('a'), server('b')]), ev({ type: 'server.deleted', server_id: 'a' }))
    expect(f.servers.map((s) => s.id)).toEqual(['b'])
    expect(f.figures.servers_total).toBe(1)
  })

  it('is the same object when nothing it shows changed', () => {
    const f = fleet([server('a')])
    expect(applyToFleet(f, ev({ type: 'server.status', server_id: 'zzz', data: { status: 'offline' } }))).toBe(f)
    expect(applyToFleet(f, ev({ type: 'task.run', server_id: 'a' }))).toBe(f)
  })
})

describe('applyToServer', () => {
  it('updates status and uptime on the server record', () => {
    const s = { id: 'a', status: 'online', online_since: 5 } as Server
    const stopped = applyToServer(s, ev({ type: 'server.status', server_id: 'a', data: { status: 'offline' } }))
    expect(stopped.status).toBe('offline')
    expect('online_since' in stopped).toBe(false)
    expect(applyToServer(s, ev({ type: 'server.status', server_id: 'b', data: { status: 'offline' } }))).toBe(s)
  })
})
