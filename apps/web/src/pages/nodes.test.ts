import { describe, expect, it } from 'vitest'
import { nodeAddress, nodeFormError, overPromised } from './nodes'

describe('nodeAddress', () => {
  it('brackets IPv6 hosts', () => {
    expect(nodeAddress({ scheme: 'http', fqdn: 'node1.lan', port: 8090 })).toBe('http://node1.lan:8090')
    expect(nodeAddress({ scheme: 'https', fqdn: '::1', port: 8443 })).toBe('https://[::1]:8443')
  })
})

describe('overPromised', () => {
  it('flags memory promised past nine tenths of the node', () => {
    expect(overPromised({ memory_mb: 8192 }, [{ ram_limit_mb: 4096 }, { ram_limit_mb: 4096 }])).toBe(8192)
    expect(overPromised({ memory_mb: 16384 }, [{ ram_limit_mb: 4096 }])).toBeNull()
    expect(overPromised({ memory_mb: null }, [{ ram_limit_mb: 4096 }])).toBeNull()
  })
})

describe('nodeFormError', () => {
  const ok = { name: 'box', fqdn: 'node1.lan', port: '8090', token: 't' }
  it('accepts a complete form', () => {
    expect(nodeFormError(ok, true)).toEqual({})
  })
  it('wants a bare host, a real port and, when asked, a token', () => {
    expect(nodeFormError({ ...ok, fqdn: 'http://node1.lan' }, true).fqdn).toBeTruthy()
    expect(nodeFormError({ ...ok, port: '70000' }, true).port).toBeTruthy()
    expect(nodeFormError({ ...ok, token: '' }, true).token).toBeTruthy()
    expect(nodeFormError({ ...ok, token: '' }, false).token).toBeUndefined()
  })
})
