import { describe, expect, it } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { routeEvent } from './wire'

function client() {
  const qc = new QueryClient()
  for (const key of [
    ['mods', 's1'],
    ['mod-updates', 's1'],
    ['backups', 's1'],
    ['mods', 's2'],
    ['mod-project', 'modrinth', 'abc'],
  ]) {
    qc.setQueryData(key, [])
  }
  return qc
}

const stale = (qc: QueryClient, key: unknown[]) => qc.getQueryState(key)?.isInvalidated

describe('routeEvent', () => {
  it('reloads a server’s data when its job finishes', () => {
    const qc = client()
    routeEvent(qc, { seq: 1, type: 'operation.updated', at: '', server_id: 's1', data: { kind: 'auto_update', status: 'succeeded' } })
    expect(stale(qc, ['mods', 's1'])).toBe(true)
    expect(stale(qc, ['mod-updates', 's1'])).toBe(true)
    expect(stale(qc, ['backups', 's1'])).toBe(true)
    expect(stale(qc, ['mods', 's2'])).toBe(false)
    expect(stale(qc, ['mod-project', 'modrinth', 'abc'])).toBe(false)
  })

  it('leaves it alone while the job runs', () => {
    const qc = client()
    routeEvent(qc, { seq: 1, type: 'operation.updated', at: '', server_id: 's1', data: { kind: 'backup', status: 'running' } })
    expect(stale(qc, ['backups', 's1'])).toBe(false)
  })
})
