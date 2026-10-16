import { describe, expect, it, vi } from 'vitest'
import type { FleetNode, FleetServer, FleetSummary, MCPActionRequest, NotificationItem } from '@/lib/types'
import { auditSentence, homeIssues } from './home'

const NOW = Date.parse('2026-10-16T12:00:00Z')
const HOUR = 3600_000

function srv(id: string, over: Partial<FleetServer> = {}): FleetServer {
  return {
    id,
    name: id,
    status: 'offline',
    platform: 'paper',
    mc_version: '1.21.4',
    node_id: 'n1',
    folder_id: null,
    sort_order: 0,
    port: 25565,
    online_since: null,
    players: null,
    tps: null,
    cpu_percent: null,
    ram_used_mb: null,
    ram_limit_mb: 4096,
    sampled_at: null,
    players_24h: [],
    last_backup_at: new Date(NOW - HOUR).toISOString(),
    backup_failed_at: null,
    backup_overdue: false,
    active_conflict: false,
    mod_updates: null,
    operation: null,
    ...over,
  }
}

function issuesFor(servers: FleetServer[], extra: { nodes?: FleetNode[]; requests?: MCPActionRequest[]; feed?: NotificationItem[] } = {}) {
  const nodes = extra.nodes ?? [{ id: 'n1', name: 'atlas', online: true } as FleetNode]
  const fleet = { servers, nodes } as FleetSummary
  return homeIssues({ fleet, servers, requests: extra.requests, feed: extra.feed ?? [], now: NOW, go: vi.fn(), start: vi.fn(), refresh: vi.fn(), markRead: vi.fn() })
}

describe('homeIssues', () => {
  it('is empty when everything is fine', () => {
    expect(issuesFor([srv('a'), srv('b', { status: 'online', tps: 20 })])).toEqual([])
  })

  it('offers a fix for a server that didn’t start, and puts problems first', () => {
    const out = issuesFor([srv('lag', { status: 'online', tps: 14.2 }), srv('broken', { status: 'crashed', active_conflict: true })])
    expect(out.map((i) => i.lead)).toEqual(['broken didn’t start.', 'lag is lagging at 14.2 TPS.'])
    expect(out[0].acts[0].label).toBe('Fix it')
  })

  it('offers a start for a plain crash', () => {
    const [i] = issuesFor([srv('a', { status: 'crashed' })])
    expect(i.lead).toBe('a crashed.')
    expect(i.acts.map((a) => a.label)).toEqual(['Start it', 'Console'])
    expect(i.acts[0].need).toBe('power.start')
  })

  it('says why backups are failing', () => {
    const [i] = issuesFor([
      srv('a', { last_backup_at: new Date(NOW - 9 * 24 * HOUR).toISOString(), backup_failed_at: new Date(NOW - HOUR).toISOString(), backup_error: 'Not enough disk space.' }),
    ])
    expect(i.lead).toBe('a hasn’t had a good backup in 9 days.')
    expect(i.rest).toBe('The last backup failed: not enough disk space.')
  })

  it('leaves a new, empty server that was never backed up alone', () => {
    expect(issuesFor([srv('fresh', { last_backup_at: null, backup_overdue: true })])).toEqual([])
    const [i] = issuesFor([srv('busy', { last_backup_at: null, backup_overdue: true, players_24h: [null, 4] })])
    expect(i.lead).toBe('busy has never been backed up.')
  })

  it('names what can’t run while a node is offline', () => {
    const out = issuesFor([srv('a'), srv('b')], {
      nodes: [{ id: 'n1', name: 'pi-garage', online: false, last_seen: new Date(NOW - 2 * HOUR).toISOString() } as FleetNode],
    })
    expect(out).toHaveLength(1)
    expect(out[0].rest).toBe('a and b can’t run until it’s back.')
  })

  it('asks about pending agent requests that haven’t expired', () => {
    const req = (id: string, expires: number, requires_password: boolean) =>
      ({ id, client_name: 'Claude Code', action: 'restart', server_name: 'Skyblock', status: 'pending', expires_at: new Date(expires).toISOString(), requires_password }) as MCPActionRequest
    const out = issuesFor([], { requests: [req('r1', NOW + HOUR, true), req('r2', NOW - HOUR, false), req('r3', NOW + HOUR, false)] })
    expect(out.map((i) => i.key)).toEqual(['reqr1', 'reqr3'])
    expect(out[0].acts.map((a) => a.label)).toEqual(['Review'])
    expect(out[1].acts.map((a) => a.label)).toEqual(['Approve', 'Deny'])
  })

  it('offers to whitelist someone the whitelist turned away', () => {
    const item = { id: 'n9', event_type: 'player.join_denied', server_id: 'a', read_at: null, data: { player: 'ember_otter', attempts: 3 } } as unknown as NotificationItem
    const [i] = issuesFor([srv('a')], { feed: [item] })
    expect(i.lead).toBe('ember_otter tried to join a three times.')
    expect(i.acts[0]).toMatchObject({ label: 'Add to whitelist', need: 'players.whitelist' })
  })
})

describe('auditSentence', () => {
  const entry = (over: object) => ({ id: 1, user_id: 'u1', api_key_id: null, server_id: 's1', server_name: 'Survival', action: 'server.restart', detail: null, ip_address: null, created_at: '', ...over })
  it('says who did what to which server', () => {
    expect(auditSentence(entry({ actor_name: 'Ana' }))).toMatchObject({ who: 'Ana', verb: 'restarted', obj: 'Survival', bad: false })
  })
  it('never names someone the viewer may not know', () => {
    expect(auditSentence(entry({})).who).toBe('Someone')
    expect(auditSentence(entry({ user_id: null, api_key_id: 'k1' })).who).toBe('An access key')
    expect(auditSentence(entry({ user_id: null })).who).toBe('MCSM')
  })
  it('makes the server the subject of a crash', () => {
    expect(auditSentence(entry({ action: 'server.crash', user_id: null }))).toMatchObject({ who: 'Survival', verb: 'crashed', bad: true })
  })
})
