import { describe, expect, it } from 'vitest'
import { PERMISSION_MODEL } from '@/lib/permissions'
import { permSummary } from './settings-access'
import { sentenceCase } from './settings-properties'

describe('permSummary', () => {
  it('names whole groups and partial ones', () => {
    expect(permSummary(['view', 'power', 'players.kick'])).toBe('View, power and some players')
    expect(permSummary(['view'])).toBe('View')
    expect(permSummary([])).toBe('Nothing')
  })
  it('calls every group everything', () => {
    expect(permSummary(PERMISSION_MODEL.map((g) => g.group))).toBe('Everything')
  })
  it('folds a complete set of leaves into its group', () => {
    expect(permSummary(['view', 'files.read', 'files.write', 'files.delete'])).toBe('View and files')
  })
})


describe('sentenceCase', () => {
  it('lowers inner words but keeps acronyms', () => {
    expect(sentenceCase('World Folder')).toBe('World folder')
    expect(sentenceCase('Bind IP')).toBe('Bind IP')
    expect(sentenceCase('Enable RCON')).toBe('Enable RCON')
  })
})
