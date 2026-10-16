import { describe, expect, it } from 'vitest'
import { parseLine, parseLines, splitAdvancement } from './lines'

const p = (raw: string, stderr = false) => parseLine(raw, { id: 1, ts: 0, stderr })

describe('parseLine', () => {
  it('reads the vanilla and Paper format', () => {
    expect(p('[12:04:31] [Server thread/INFO]: Done (3.2s)! For help, type "help"')).toMatchObject({
      time: '12:04:31',
      level: 'INFO',
      kind: 'log',
      text: 'Done (3.2s)! For help, type "help"',
    })
    expect(p('\x1b[33m[12:04:31] [Server thread/WARN]: Can\'t keep up!\x1b[m')).toMatchObject({ level: 'WARN', text: "Can't keep up!" })
  })

  it('reads Paper’s console format', () => {
    expect(p('[01:11:22 INFO]: Done (19.482s)! For help, type "help"')).toMatchObject({ time: '01:11:22', level: 'INFO', text: 'Done (19.482s)! For help, type "help"' })
    expect(p('[01:11:22 WARN]: [spark] The async-profiler engine is not supported').level).toBe('WARN')
    expect(p('[01:12:00 INFO]: Ana joined the game').kind).toBe('join')
  })

  it('reads the Forge format', () => {
    expect(p('[16Oct2026 12:04:31.123] [main/ERROR] [net.minecraft.server.Main/]: Failed to start')).toMatchObject({
      time: '12:04:31',
      level: 'ERROR',
      text: 'Failed to start',
    })
  })

  it('tells chat, joins and the panel’s own notes apart', () => {
    expect(p('[12:00:00] [Server thread/INFO]: <Ana> hello').kind).toBe('chat')
    expect(p('[12:00:00] [Server thread/INFO]: Ana joined the game').kind).toBe('join')
    expect(p('[mcsm] helper mod installed')).toMatchObject({ kind: 'sys', text: 'helper mod installed', level: 'INFO' })
    expect(p('[mcsm] warning: could not set server-port').level).toBe('WARN')
  })

  it('keeps a stack trace at the level of its error', () => {
    const lines = parseLines([
      '[12:00:00] [main/ERROR]: Exception in server tick loop',
      'java.lang.OutOfMemoryError: Java heap space',
      '\tat net.minecraft.server.MinecraftServer.run(MinecraftServer.java:100)',
      '[12:00:01] [main/INFO]: Stopping server',
      '\tat somewhere.else(Unrelated.java:1)',
    ])
    expect(lines.map((l) => l.level)).toEqual(['ERROR', 'INFO', 'INFO', 'INFO', 'INFO'])
    const traced = parseLines(['[12:00:00] [main/ERROR]: Exception in server tick loop', '\tat net.minecraft.server.MinecraftServer.run(MinecraftServer.java:100)'])
    expect(traced[1].level).toBe('ERROR')
  })

  it('judges bare stderr by what it says', () => {
    expect(p('Exception in thread "main"', true).level).toBe('ERROR')
    expect(p('SLF4J: No providers were found.', true).level).toBe('WARN')
    expect(p('Loading libraries, please wait...').level).toBe('INFO')
  })
})

describe('splitAdvancement', () => {
  it('sets the advancement apart', () => {
    expect(splitAdvancement('Ana has made the advancement [Stone Age]')).toEqual(['Ana has made the advancement ', '[Stone Age]'])
    expect(splitAdvancement('Ana joined the game')).toBeNull()
  })
})
