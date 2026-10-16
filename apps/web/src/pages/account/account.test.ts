import { describe, expect, it } from 'vitest'
import { loosened, scopeSentence } from './agents'
import { expiryIn, keyState } from './keys'
import { deviceLabel, passwordError } from './you'

describe('deviceLabel', () => {
  it('names the browser and system', () => {
    expect(deviceLabel('Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0 Safari/537.36')).toBe('Chrome on Windows')
    expect(deviceLabel('Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15')).toBe('Safari on macOS')
    expect(deviceLabel('Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0')).toBe('Firefox on Linux')
    expect(deviceLabel('Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/128.0 Safari/537.36 Edg/128.0')).toBe('Edge on Windows')
    expect(deviceLabel('')).toBe('An unknown device')
  })
})

describe('passwordError', () => {
  it('wants ten characters and a match', () => {
    expect(passwordError('short', '')).not.toBeNull()
    expect(passwordError('long enough pw', 'long enough pv')).not.toBeNull()
    expect(passwordError('long enough pw', 'long enough pw')).toBeNull()
    expect(passwordError('', '')).toBeNull()
  })
})

describe('access keys', () => {
  it('expire at the end of the chosen day', () => {
    const at = new Date(expiryIn(30, new Date(2026, 0, 1, 15, 0)))
    expect([at.getFullYear(), at.getMonth(), at.getDate(), at.getHours(), at.getMinutes()]).toEqual([2026, 0, 31, 23, 59])
  })
  it('are revoked, expired or live', () => {
    const now = Date.parse('2026-06-01T00:00:00Z')
    expect(keyState({ revoked_at: '2026-05-01T00:00:00Z', expires_at: '2026-07-01T00:00:00Z' }, now)).toBe('revoked')
    expect(keyState({ revoked_at: null, expires_at: '2026-05-01T00:00:00Z' }, now)).toBe('expired')
    expect(keyState({ revoked_at: null, expires_at: '2026-07-01T00:00:00Z' }, now)).toBe('live')
  })
})

describe('agents', () => {
  it('splits what an agent reads from what it does alone', () => {
    expect(scopeSentence(['mcp:servers.read', 'mcp:logs.read', 'mcp:power.restart'])).toEqual({ reads: 'see servers and read warnings and errors', acts: 'restart servers' })
  })
  it('knows which changes loosen the rules', () => {
    const strict = { require_password: true, auto_approve_lifecycle: false, auto_approve_upgrades: false }
    expect(loosened(strict, { ...strict, require_password: false }).map((r) => r.key)).toEqual(['require_password'])
    expect(loosened(strict, { ...strict, auto_approve_upgrades: true }).map((r) => r.key)).toEqual(['auto_approve_upgrades'])
    expect(loosened({ ...strict, auto_approve_lifecycle: true }, strict)).toEqual([])
  })
})
