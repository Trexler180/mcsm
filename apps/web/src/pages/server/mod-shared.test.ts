import { describe, expect, it } from 'vitest'
import { contradicts, fitsServer, pageWindow, sideNote, topicsOf } from './mod-shared'

const sides = (client_side: string, server_side: string) => ({ client_side, server_side })

describe('sideNote', () => {
  it('says where a project runs', () => {
    expect(sideNote(sides('unsupported', 'required'))?.text).toBe('Server only.')
    expect(sideNote(sides('required', 'required'))?.text).toBe('Players need it too.')
    expect(sideNote(sides('optional', 'optional'))?.text).toBe('Players can add it too.')
    expect(sideNote(sides('required', 'unsupported'))).toMatchObject({ warn: true })
  })
  it('says nothing when the source doesn’t', () => {
    expect(sideNote(sides('', ''))).toBeNull()
  })
})

describe('contradicts', () => {
  it('leaves out what plainly isn’t the side asked for', () => {
    expect(contradicts(sides('required', 'required'), 'server_only')).toBe(true)
    expect(contradicts(sides('optional', 'required'), 'server_only')).toBe(false)
    expect(contradicts(sides('unsupported', 'required'), 'client_server')).toBe(true)
    expect(contradicts(sides('required', 'unsupported'), '')).toBe(true)
    expect(contradicts(sides('unsupported', 'required'), 'client')).toBe(true)
  })
  it('keeps everything for anywhere, and anything without sides', () => {
    expect(contradicts(sides('required', 'unsupported'), 'any')).toBe(false)
    expect(contradicts(sides('', ''), 'server_only')).toBe(false)
  })
})

describe('fitsServer', () => {
  const hit = { versions: ['1.21.3', '1.21.4'], categories: ['fabric', 'utility'] }
  it('needs the version and, for mods on Modrinth, the loader', () => {
    expect(fitsServer(hit, 'mod', 'modrinth', 'fabric', '1.21.4')).toBe(true)
    expect(fitsServer(hit, 'mod', 'modrinth', 'forge', '1.21.4')).toBe(false)
    expect(fitsServer(hit, 'mod', 'modrinth', 'fabric', '1.20.1')).toBe(false)
  })
  it('doesn’t judge by what a source can’t say', () => {
    expect(fitsServer(hit, 'plugin', 'spigotmc', 'paper', '1.20.1')).toBe(true)
    expect(fitsServer({ versions: [], categories: [] }, 'mod', 'curseforge', 'fabric', '1.21.4')).toBe(true)
  })
})

describe('pageWindow', () => {
  it('centres the current page in up to five', () => {
    expect(pageWindow(0, 3)).toEqual([0, 1, 2])
    expect(pageWindow(0, 20)).toEqual([0, 1, 2, 3, 4])
    expect(pageWindow(10, 20)).toEqual([8, 9, 10, 11, 12])
    expect(pageWindow(19, 20)).toEqual([15, 16, 17, 18, 19])
  })
})

describe('topicsOf', () => {
  it('drops loader names from categories', () => {
    expect(topicsOf(['bukkit', 'folia', 'optimization', 'fabric'])).toEqual(['optimization'])
  })
})
