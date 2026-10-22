import { describe, expect, it } from 'vitest'
import { parseProperties, propertyEdits, rebaseProperties, serializeProperties } from './properties-schema'

describe('saving server.properties from a page that loaded it earlier', () => {
  const loaded = parseProperties('#Minecraft server properties\nserver-port=25565\nmotd=Hi\n')
  const draft = { ...loaded, 'view-distance': '7' }
  // Saved by someone else after this page loaded the file.
  const now = '#Minecraft server properties\nserver-port=25565\nmotd=Hi\nmax-players=33\n'

  it('writes only its own edits, keeping what was saved since', () => {
    const out = parseProperties(serializeProperties(now, propertyEdits(loaded, draft)))
    expect(out['view-distance']).toBe('7')
    expect(out['max-players']).toBe('33')
    expect(out['motd']).toBe('Hi')
  })

  it('takes a newer copy of the file under the edits being made', () => {
    const next = parseProperties(now)
    const rebased = rebaseProperties(loaded, draft, next)
    expect(rebased['max-players']).toBe('33')
    expect(rebased['view-distance']).toBe('7')
    expect(propertyEdits(next, rebased)).toEqual({ 'view-distance': '7' })
  })

  it('keeps an edit to a key the other save also changed', () => {
    const mine = { ...loaded, motd: 'Mine' }
    expect(rebaseProperties(loaded, mine, parseProperties(now.replace('motd=Hi', 'motd=Theirs'))).motd).toBe('Mine')
  })
})
