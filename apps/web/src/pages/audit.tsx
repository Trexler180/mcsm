import { Fragment, useState } from 'react'
import { useInfiniteQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { AuditEntryWithActor } from '@/lib/types'
import { useAuthStore } from '@/store/auth'
import { useFleet } from '@/shell/fleet'
import { Btn, Empty, Filters, Search, Select } from '@/ui/controls'
import { fmtClock, fmtDay } from '@/ui/format'
import { PageHead } from '@/ui/page'
import { Loaded, SkItems } from '@/ui/skeleton'
import { toast } from '@/ui/toasts'
import { cx, plural, toMs } from '@/ui/util'
import { auditSentence } from './audit-text'

// Every change and who made it: people, schedules, access keys and agents.
// Administrators see everything; anyone else sees what happened on the
// servers they can open, plus their own sign-ins.

const KINDS = [
  { value: '', label: 'Everything' },
  { value: 'server.', label: 'Servers' },
  { value: 'player.', label: 'Players' },
  { value: 'mod.', label: 'Mods' },
  { value: 'backup.', label: 'Backups' },
  { value: 'file.', label: 'Files' },
  { value: 'auth.', label: 'Sign-ins' },
  { value: 'mcp.', label: 'Agents' },
] as const
type Kind = (typeof KINDS)[number]['value']

const PAGE = 100

function scalar(v: unknown): string {
  if (v == null) return '–'
  if (typeof v === 'string') return v || '“”'
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  if (Array.isArray(v)) return v.map(scalar).join(', ')
  return JSON.stringify(v)
}

/** An entry's detail as short lines: "role: user → admin", "command: say hi". */
export function detailLines(detail: string | null): string[] {
  if (!detail) return []
  let d: unknown
  try {
    d = JSON.parse(detail)
  } catch {
    return [detail]
  }
  if (typeof d !== 'object' || d === null || Array.isArray(d)) return [scalar(d)]
  const out: string[] = []
  for (const [k, v] of Object.entries(d as Record<string, unknown>)) {
    const label = k.replace(/_/g, ' ')
    if (k === 'changes' && v && typeof v === 'object' && !Array.isArray(v)) {
      for (const [field, change] of Object.entries(v as Record<string, unknown>)) {
        const c = change as { from?: unknown; to?: unknown } | null
        out.push(c && typeof c === 'object' && ('from' in c || 'to' in c) ? `${field.replace(/_/g, ' ')}: ${scalar(c.from)} → ${scalar(c.to)}` : `${field.replace(/_/g, ' ')}: ${scalar(change)}`)
      }
    } else {
      out.push(`${label}: ${scalar(v)}`)
    }
  }
  return out
}

function csvCell(v: string): string {
  return /[",\n\r]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v
}

/** The loaded entries as CSV, for a spreadsheet. */
export function auditCsv(entries: AuditEntryWithActor[]): string {
  const rows = [['time', 'who', 'action', 'server', 'detail', 'address']]
  for (const e of entries) {
    const t = auditSentence(e)
    rows.push([e.created_at, t.who, e.action, e.server_name ?? '', detailLines(e.detail).join('; '), e.ip_address ?? ''])
  }
  return rows.map((r) => r.map(csvCell).join(',')).join('\r\n') + '\r\n'
}

export function AuditPage() {
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  const fleet = useFleet().data
  const [kind, setKind] = useState<Kind>('')
  const [server, setServer] = useState('')
  const [q, setQ] = useState('')
  const [open, setOpen] = useState<number | null>(null)
  const log = useInfiniteQuery({
    queryKey: ['audit-log', kind, server],
    queryFn: ({ pageParam }) => api.audit.page({ action: kind, server_id: server, before: pageParam, limit: PAGE }),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (last) => last.next_cursor,
  })
  const all = log.data?.pages.flatMap((p) => p.entries) ?? []
  const list = q
    ? all.filter((e) => {
        const t = auditSentence(e)
        return `${t.who} ${t.verb} ${t.obj} ${e.action} ${detailLines(e.detail).join(' ')}`.toLowerCase().includes(q.toLowerCase())
      })
    : all

  const exportCsv = () => {
    const blob = new Blob([auditCsv(list)], { type: 'text/csv;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `mcsm-audit-${new Date().toISOString().slice(0, 10)}.csv`
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
    toast('ok', `Exported ${plural(list.length, 'entry', 'entries')}`, log.hasNextPage ? 'Only what’s loaded. Load older entries first to include them.' : undefined)
  }

  return (
    <div className="scroll">
      <div className="page narrow">
        <PageHead title="Audit log" lede={admin ? 'Every change, and who made it: people, schedules, access keys and agents.' : 'What happened on the servers you can open, and your own sign-ins.'}>
          <Btn disabled={!list.length} onClick={exportCsv}>
            Export
          </Btn>
        </PageHead>
        <div className="toolbar" style={{ flexWrap: 'wrap' }}>
          <Search value={q} onChange={setQ} placeholder="Filter what’s loaded" style={{ width: 220 }} />
          <Select
            value={server}
            onChange={setServer}
            aria-label="Server"
            style={{ width: 'auto' }}
            options={[{ value: '', label: 'Every server' }, ...(fleet?.servers ?? []).map((s) => ({ value: s.id, label: s.name }))]}
          />
          <Filters value={kind} onChange={setKind} label="Kind" options={KINDS.map((k) => ({ value: k.value, label: k.label }))} />
        </div>
        <Loaded query={log} sk={<SkItems rows={8} />} what="the audit log">
          {() => {
            let lastDay = ''
            return list.length === 0 ? (
              <Empty title={all.length ? 'Nothing loaded matches.' : 'Nothing here yet.'} />
            ) : (
              <>
                <div className="items audit-list">
                  {list.map((e) => {
                    const at = toMs(e.created_at) ?? 0
                    const day = fmtDay(at)
                    const head = day !== lastDay ? day : null
                    lastDay = day
                    const t = auditSentence(e)
                    const lines = detailLines(e.detail)
                    const expanded = open === e.id
                    return (
                      <Fragment key={e.id}>
                        {head && <div className="item day t3 small">{head}</div>}
                        <div className={cx('item audit-row', lines.length > 0 && 'click')} onClick={() => lines.length && setOpen(expanded ? null : e.id)} aria-expanded={lines.length ? expanded : undefined}>
                          <span className="num t3 audit-time">{fmtClock(at)}</span>
                          <div className="grow" style={{ minWidth: 0 }}>
                            <div>
                              <span className={cx('strong', t.bad && 'bad')}>{t.who}</span> {t.verb}
                              {t.obj ? <> {t.obj}</> : null}
                              {e.api_key_id && !e.actor_name && <span className="t3"> (access key)</span>}
                            </div>
                            {lines.length > 0 && !expanded && <div className="t3 small ellipsis">{lines.join(', ')}</div>}
                            {expanded && (
                              <div className="audit-detail">
                                {lines.map((l, i) => (
                                  <div key={i} className="mono small">
                                    {l}
                                  </div>
                                ))}
                                <div className="t3 small" style={{ marginTop: 6 }}>
                                  {e.action}
                                  {e.ip_address ? `, from ${e.ip_address}` : ''}
                                </div>
                              </div>
                            )}
                          </div>
                        </div>
                      </Fragment>
                    )
                  })}
                </div>
                {log.hasNextPage && (
                  <div className="row" style={{ justifyContent: 'center', marginTop: 16 }}>
                    <Btn loading={log.isFetchingNextPage} onClick={() => void log.fetchNextPage()}>
                      Load older
                    </Btn>
                  </div>
                )}
              </>
            )
          }}
        </Loaded>
      </div>
    </div>
  )
}
