import { describe, expect, it } from 'vitest'
import type { NotificationChannel, NotificationItem } from '@/lib/types'
import { alertLink, channelsFor, webhookUrlError } from './alerts'

const alert = (event_type: string, server_id: string | null = 's1'): NotificationItem => ({
  id: 'n1',
  user_id: 'u',
  event_type,
  severity: 'warning',
  server_id,
  node_id: null,
  title: 't',
  body: '',
  data: {},
  dedupe_key: '',
  created_at: '2026-10-16T00:00:00Z',
  read_at: null,
})

describe('alertLink', () => {
  it('points each kind of alert at where it can be dealt with', () => {
    expect(alertLink(alert('server.crash'))).toEqual({ label: 'Console', to: '/s/s1' })
    expect(alertLink(alert('mod.update_failed'))).toEqual({ label: 'Mods', to: '/s/s1/mods' })
    expect(alertLink(alert('backup.failed'))?.to).toBe('/s/s1/backups')
    expect(alertLink(alert('schedule.failed'))?.to).toBe('/s/s1/schedules')
    expect(alertLink(alert('server.version_change_failed'))).toEqual({ label: 'Version', to: '/s/s1/settings?sub=version' })
    expect(alertLink(alert('node.offline', null))?.label).toBe('Nodes')
    expect(alertLink(alert('auth.new_device', null))?.label).toBe('Sessions')
  })
  it('offers nothing when there’s nothing to do', () => {
    expect(alertLink(alert('server.online'))).toBeNull()
    expect(alertLink(alert('server.crash', null))).toBeNull()
  })
})

describe('channelsFor', () => {
  it('lists the panel, push and each webhook, marking ones turned off', () => {
    const hook = { id: 'h1', label: 'Staff', enabled: false } as NotificationChannel
    expect(channelsFor([hook])).toEqual([
      { id: 'inapp', label: 'Panel' },
      { id: 'webpush', label: 'Push' },
      { id: 'webhook:h1', label: 'Staff', off: true },
    ])
  })
})

describe('webhookUrlError', () => {
  it('wants an https address', () => {
    expect(webhookUrlError('')).toBeNull()
    expect(webhookUrlError('https://discord.com/api/webhooks/1/abc')).toBeNull()
    expect(webhookUrlError('http://example.com/hook')).not.toBeNull()
    expect(webhookUrlError('not a url')).not.toBeNull()
  })
})
