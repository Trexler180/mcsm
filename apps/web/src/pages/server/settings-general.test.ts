import { describe, expect, it } from 'vitest'
import type { Server } from '@/lib/types'
import { packOf } from './settings-general'

const server = (settings: Record<string, unknown> = {}) => ({ id: 's1', settings }) as unknown as Server

describe('packOf', () => {
  it('reads the same for every refresh of a server with no pack yet', () => {
    // A new random id per refresh used to leave "1 unsaved change" after every save.
    expect(packOf(server(), 'fresh')).toEqual(packOf(server(), 'fresh'))
    expect(packOf(server(), 'fresh').public_id).toBe('fresh')
  })
  it('keeps the id a saved pack already has', () => {
    expect(packOf(server({ resource_pack: { public_id: 'saved', enabled: true } }), 'fresh').public_id).toBe('saved')
  })
})
