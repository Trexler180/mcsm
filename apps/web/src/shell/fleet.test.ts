import { describe, expect, it } from 'vitest'
import type { FleetServer, FleetSummary, ServerFolder } from '@/lib/types'
import { effStatus, groupFleet, placeServer, withPlacement } from './fleet'

const srv = (id: string, folder_id: string | null, sort_order: number, over: Partial<FleetServer> = {}) =>
  ({ id, name: id, folder_id, sort_order, status: 'offline', node_id: 'n1', ...over }) as FleetServer
const folder = (id: string, sort_order: number) => ({ id, name: id, sort_order }) as ServerFolder
const fleetOf = (servers: FleetServer[]) => ({ servers, nodes: [] }) as unknown as FleetSummary

describe('groupFleet', () => {
  it('orders folders and their servers, and puts the rest last', () => {
    const groups = groupFleet(
      [srv('c', 'f1', 1), srv('a', 'f1', 0), srv('x', null, 0), srv('lost', 'gone', 0), srv('b', 'f2', 0)],
      [folder('f2', 1), folder('f1', 0)],
    )
    expect(groups.map((g) => [g.folder?.id ?? null, g.servers.map((s) => s.id)])).toEqual([
      ['f1', ['a', 'c']],
      ['f2', ['b']],
      [null, ['lost', 'x']],
    ])
  })
})

describe('placeServer', () => {
  const folders = [folder('f1', 0)]
  const fleet = fleetOf([srv('a', 'f1', 0), srv('b', 'f1', 1), srv('c', 'f1', 2), srv('x', null, 0)])

  it('moves a server down within its folder', () => {
    expect(placeServer(fleet, folders, 'a', 'f1', 3)).toEqual([
      { id: 'b', folder_id: 'f1', sort_order: 0 },
      { id: 'c', folder_id: 'f1', sort_order: 1 },
      { id: 'a', folder_id: 'f1', sort_order: 2 },
    ])
  })

  it('moves a server into another folder at a position', () => {
    expect(placeServer(fleet, folders, 'x', 'f1', 1).map((p) => p.id)).toEqual(['a', 'x', 'b', 'c'])
  })

  it('takes a server out of its folder', () => {
    expect(placeServer(fleet, folders, 'b', null, 0)).toEqual([
      { id: 'b', folder_id: null, sort_order: 0 },
      { id: 'x', folder_id: null, sort_order: 1 },
    ])
  })

  it('applies a placement to the fleet', () => {
    const moved = withPlacement(fleet, placeServer(fleet, folders, 'x', 'f1', 0))
    expect(moved.servers.find((s) => s.id === 'x')).toMatchObject({ folder_id: 'f1', sort_order: 0 })
  })
})

describe('effStatus', () => {
  const nodes = [{ id: 'n1', online: false }] as FleetSummary['nodes']
  it('says node offline rather than stopped or unknown', () => {
    expect(effStatus(srv('a', null, 0, { status: 'unknown' }), [])).toBe('unreachable')
    expect(effStatus(srv('a', null, 0, { status: 'offline' }), nodes)).toBe('unreachable')
    expect(effStatus(srv('a', null, 0, { status: 'online' }), nodes)).toBe('online')
  })
})
