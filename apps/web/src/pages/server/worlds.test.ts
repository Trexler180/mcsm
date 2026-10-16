import { describe, expect, it } from 'vitest'
import type { FileTree } from '@/lib/types'
import { findWorlds, folderError, folderFromZip } from './worlds'

const tree = (paths: string[], sizes?: Record<string, number>): FileTree => ({
  path: '/',
  truncated: false,
  sizes,
  entries: paths.map((path) => ({ path, type: 'file', size: 0, modified: '2026-10-01T12:00:00Z' })),
})

describe('findWorlds', () => {
  it('finds folders holding a level.dat and folds in their Nether and End', () => {
    const rows = findWorlds(
      tree(['server.jar', 'world/level.dat', 'world_nether/level.dat', 'world_the_end/level.dat', 'creative/level.dat', 'plugins/x.jar'], {
        world: 100,
        world_nether: 20,
        world_the_end: 5,
        creative: 50,
      }),
      'world',
    )
    expect(rows.map((r) => r.folder)).toEqual(['world', 'creative'])
    expect(rows[0]).toMatchObject({ linked: ['world_nether', 'world_the_end'], size: 125 })
    expect(rows[1]).toMatchObject({ linked: [], size: 50 })
  })

  it('puts the loaded world first and leaves sizes out when the node can’t say', () => {
    const rows = findWorlds(tree(['alpha/level.dat', 'zeta/level.dat']), 'zeta')
    expect(rows.map((r) => r.folder)).toEqual(['zeta', 'alpha'])
    expect(rows[0].size).toBeUndefined()
  })

  it('keeps a lone Nether folder as a world of its own', () => {
    expect(findWorlds(tree(['old_nether/level.dat']), 'world').map((r) => r.folder)).toEqual(['old_nether'])
  })
})

describe('world folder names', () => {
  it('comes from the zip’s name', () => {
    expect(folderFromZip('My World (1).zip')).toBe('My World')
    expect(folderFromZip('a:b.zip')).toBe('a b')
  })
  it('refuses what the node would', () => {
    expect(folderError('')).not.toBeNull()
    expect(folderError('.hidden')).not.toBeNull()
    expect(folderError('a/b')).not.toBeNull()
    expect(folderError('Island Map')).toBeNull()
  })
})
