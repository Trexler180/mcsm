import { describe, expect, it } from 'vitest'
import { freePort, nameFromFolder, promisedOn } from './new-server'

const servers = [
  { node_id: 'a', port: 25565, ram_limit_mb: 4096 },
  { node_id: 'a', port: 25566, ram_limit_mb: 2048 },
  { node_id: 'b', port: 25565, ram_limit_mb: 8192 },
]

describe('new server defaults', () => {
  it('finds the next free port on the node', () => {
    expect(freePort('a', servers)).toBe(25567)
    expect(freePort('b', servers)).toBe(25566)
    expect(freePort('c', servers)).toBe(25565)
  })
  it('totals memory promised on a node', () => {
    expect(promisedOn('a', servers)).toBe(6144)
    expect(promisedOn('c', servers)).toBe(0)
  })
  it('makes a name from a folder', () => {
    expect(nameFromFolder('my-old_smp')).toBe('My old smp')
    expect(nameFromFolder('/srv/minecraft/creative')).toBe('Creative')
  })
})
