import { describe, expect, it } from 'vitest'
import type { AuditEntryWithActor } from '@/lib/types'
import { auditCsv, detailLines } from './audit'
import { auditSentence } from './audit-text'

const entry = (over: Partial<AuditEntryWithActor>): AuditEntryWithActor => ({
  id: 1,
  user_id: 'u1',
  api_key_id: null,
  server_id: 's1',
  server_name: 'Survival',
  action: 'server.restart',
  detail: null,
  ip_address: '203.0.113.5',
  created_at: '2026-10-16T12:00:00Z',
  actor_name: 'Ana',
  ...over,
})

describe('detailLines', () => {
  it('reads changes as from → to', () => {
    expect(detailLines(JSON.stringify({ changes: { role: { from: 'user', to: 'admin' } }, email: 'a@example.com' }))).toEqual(['role: user → admin', 'email: a@example.com'])
  })
  it('copes with plain text and nothing', () => {
    expect(detailLines(null)).toEqual([])
    expect(detailLines('not json')).toEqual(['not json'])
    expect(detailLines(JSON.stringify({ files: ['a', 'b'] }))).toEqual(['files: a, b'])
  })
})

describe('auditCsv', () => {
  it('quotes what needs quoting', () => {
    const csv = auditCsv([entry({ detail: JSON.stringify({ command: 'say "hi", all' }) })])
    expect(csv.split('\r\n')[0]).toBe('time,who,action,server,detail,address')
    expect(csv).toContain('"command: say ""hi"", all"')
  })
})

describe('auditSentence for panel actions', () => {
  it('names no server for things about people or the panel', () => {
    expect(auditSentence(entry({ action: 'user.create' }))).toMatchObject({ who: 'Ana', verb: 'added a person', obj: '' })
    expect(auditSentence(entry({ action: 'auth.api_key.create' })).obj).toBe('')
    expect(auditSentence(entry({ action: 'mod.upload' }))).toMatchObject({ verb: 'uploaded a mod to', obj: 'Survival' })
  })
  it('marks failures', () => {
    expect(auditSentence(entry({ action: 'mod.autoupdate_failed' })).bad).toBe(true)
    expect(auditSentence(entry({ action: 'node.reboot.denied' })).bad).toBe(true)
  })
})
