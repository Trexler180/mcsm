import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { Backup, BackupTarget, Server, ScheduledTask } from '@/lib/types'
import { Btn, Empty, Field, TextBtn, type StatusKey } from '@/ui/controls'
import { describeCron } from '@/ui/cron'
import { act } from '@/ui/failure'
import { fmtBytes, fmtClock, fmtDate, fmtDay } from '@/ui/format'
import { MenuButton, MenuItem, Sheet } from '@/ui/overlays'
import { contentLabel } from '@/ui/platforms'
import { Loaded, SkTable } from '@/ui/skeleton'
import { ask } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { plural, toMs } from '@/ui/util'

// Backups: copies of the whole server folder, made by hand, on a schedule,
// or before something risky. Restoring replaces the folder with one.

const DEFAULT_KEEP = 10

interface Policy {
  keep_last_n?: number
  max_age_days?: number
}

const policyOf = (t: BackupTarget | undefined): Policy => (t?.retention as Policy | null) ?? {}

function keepSentence(p: Policy): string {
  const n = p.keep_last_n || (!p.max_age_days ? DEFAULT_KEEP : 0)
  if (n && p.max_age_days) return `keeping the last ${n}, none older than ${plural(p.max_age_days, 'day')}`
  if (n) return `keeping the last ${n}`
  return `keeping ${plural(p.max_age_days ?? 0, 'day')} of them`
}

const TRIGGER: Record<string, string> = {
  scheduled: 'Scheduled',
  auto_update: 'Before updating mods safely',
  migration: 'Before a version change',
  pre_restore: 'Before restoring another backup',
  manual: 'Made by hand',
}

interface Row {
  b: Backup
  n: number
  first: number
}

/** Consecutive scheduled failures with the same reason read as one row. */
export function groupBackups(list: Backup[]): Row[] {
  const rows: Row[] = []
  for (const b of list) {
    const prev = rows[rows.length - 1]
    const started = toMs(b.started_at) ?? 0
    const scheduled = (x: Backup) => x.trigger === 'scheduled' || x.trigger === 'schedule'
    if (prev && b.status === 'failed' && prev.b.status === 'failed' && scheduled(b) && scheduled(prev.b) && b.metadata?.error === prev.b.metadata?.error) {
      prev.n++
      prev.first = started
    } else rows.push({ b, n: 1, first: started })
  }
  return rows
}

export function BackupsTab({ server, status }: { server: Server; status: StatusKey }) {
  const backups = useQuery({
    queryKey: ['backups', server.id],
    queryFn: () => api.backups.list(server.id),
    refetchInterval: (q) => (q.state.data?.some((b) => b.status === 'running') ? 3000 : false),
  })
  return (
    <Loaded query={backups} sk={<SkTable rows={6} />} what={`backups of ${server.name}`}>
      {(list) => <BackupsBody server={server} status={status} list={list} />}
    </Loaded>
  )
}

function BackupsBody({ server, status, list }: { server: Server; status: StatusKey; list: Backup[] }) {
  const qc = useQueryClient()
  const [keepOpen, setKeepOpen] = useState(false)
  const tasks = useQuery({ queryKey: ['tasks', server.id], queryFn: () => api.tasks.list(server.id), staleTime: 60_000, retry: false }).data
  const targets = useQuery({ queryKey: ['backup-targets', server.id], queryFn: () => api.backups.targets.list(server.id), staleTime: 60_000, retry: false }).data
  const sched: ScheduledTask | undefined = tasks?.find((t) => t.action === 'backup' && t.enabled)
  const target = targets?.find((t) => t.is_default) ?? targets?.[0]
  const ok = list.filter((b) => b.status === 'success')
  const total = ok.reduce((n, b) => n + (b.size_bytes ?? 0), 0)
  const running = list.some((b) => b.status === 'running')
  const live = status === 'online' || status === 'starting'
  const refresh = () => void qc.invalidateQueries({ queryKey: ['backups', server.id] })

  const backUp = () =>
    void act(`back up ${server.name}`, () => api.backups.create(server.id)).then((r) => {
      if (r === undefined) return
      toast('info', `Backing up ${server.name}`)
      refresh()
    })
  const nightly = () =>
    void act('schedule a nightly backup', () => api.tasks.create(server.id, { name: 'Nightly backup', cron_expr: '0 4 * * *', action: 'backup', enabled: true })).then((r) => {
      if (r === undefined) return
      toast('ok', `${server.name} backs up every night at 04:00`)
      void qc.invalidateQueries({ queryKey: ['tasks', server.id] })
    })
  const restore = async (b: Backup) => {
    const at = toMs(b.started_at) ?? 0
    const ok = await ask({
      title: `Restore ${server.name} to ${fmtDay(at).toLowerCase() === 'today' ? 'today' : fmtDate(at, false)} at ${fmtClock(at)}?`,
      body: `Everything in its folder is replaced with this backup, so anything built since then is lost. A backup of how it is now is made first.${live ? ' The server is stopped first and started again afterwards.' : ''}`,
      confirm: 'Restore',
      danger: true,
    })
    if (!ok) return
    const r = await act(`restore ${server.name}`, () => api.backups.restore(server.id, b.id))
    if (r === undefined) return
    toast('info', `Restoring ${server.name}`, 'It stays stopped until it’s done.')
    refresh()
  }
  const remove = async (b: Backup) => {
    const at = toMs(b.started_at) ?? 0
    if (!(await ask({ title: 'Delete this backup?', body: `${fmtDate(at)}${b.size_bytes ? `, ${fmtBytes(b.size_bytes)}` : ''}. This can’t be undone.`, confirm: 'Delete', danger: true }))) return
    const r = await act('delete the backup', () => api.backups.delete(server.id, b.id).then(() => true))
    if (r === undefined) return
    toast('ok', 'Deleted the backup')
    refresh()
  }

  return (
    <>
      <div className="toolbar" style={{ marginBottom: 20, alignItems: 'flex-start' }}>
        <div className="grow">
          <p className="t2" style={{ maxWidth: '68ch' }}>
            {sched ? (
              <>
                Backed up {describeCron(sched.cron_expr).toLowerCase().replace('every day', 'every night')}, {keepSentence(policyOf(target))}.
              </>
            ) : (
              <>
                <span className="warn">Nothing backs up {server.name} automatically.</span> Made by hand, {keepSentence(policyOf(target))}.
              </>
            )}
            {ok.length > 0 && (
              <>
                {' '}
                {plural(ok.length, 'backup')} use {fmtBytes(total)}.
              </>
            )}{' '}
            <TextBtn quiet need="backups.delete" onClick={() => setKeepOpen(true)}>
              Change how many are kept
            </TextBtn>
          </p>
        </div>
        {!sched && (
          <Btn need="tasks" onClick={nightly}>
            Back up every night
          </Btn>
        )}
        <Btn variant="primary" need="backups.create" loading={running} onClick={backUp}>
          Back up now
        </Btn>
      </div>
      {list.length === 0 ? (
        <Empty title="No backups yet.">A backup is a copy of the whole server folder: worlds, settings and {contentLabel(server.platform).toLowerCase()}.</Empty>
      ) : (
        <table className="t">
          <tbody>
            {groupBackups(list).map(({ b, n, first }) => {
              const at = toMs(b.started_at) ?? 0
              const why = b.metadata?.error
              return (
                <tr key={b.id}>
                  <td className="nowrap" style={{ width: 150 }}>
                    {fmtDay(at)} <span className="t3 num">{fmtClock(at)}</span>
                  </td>
                  <td>
                    {b.status === 'failed' ? (
                      <>
                        <span className="bad">{n > 1 ? `Failed every time since ${fmtDate(first, false)}.` : 'Failed.'}</span>
                        {why && <span className="t2"> {why.charAt(0).toUpperCase() + why.slice(1).replace(/\.?$/, '.')}</span>}
                      </>
                    ) : b.status === 'running' ? (
                      <span className="t2">Writing the archive…</span>
                    ) : (
                      TRIGGER[b.trigger] ?? 'Made by hand'
                    )}
                  </td>
                  <td className="r num t2 nowrap">{b.size_bytes ? fmtBytes(b.size_bytes) : ''}</td>
                  <td className="actions">
                    {b.status === 'success' && (
                      <span className="row" style={{ gap: 2 }}>
                        <span className="hover-show">
                          <Btn size="sm" need="backups.restore" onClick={() => void restore(b)}>
                            Restore
                          </Btn>
                        </span>
                        <MenuButton
                          label="More"
                          items={(close) => (
                            <>
                              <MenuItem close={close} need="files.read" onClick={() => void act('download the backup', () => api.backups.download(server.id, b.id))}>
                                Download
                              </MenuItem>
                              <MenuItem danger close={close} need="backups.delete" onClick={() => void remove(b)}>
                                Delete
                              </MenuItem>
                            </>
                          )}
                        />
                      </span>
                    )}
                    {b.status === 'failed' && (
                      <Btn size="sm" need="backups.create" onClick={backUp}>
                        Try again
                      </Btn>
                    )}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      <KeepSheet open={keepOpen} onClose={() => setKeepOpen(false)} server={server} target={target} />
    </>
  )
}

/** How many backups are kept, and for how long. */
function KeepSheet({ open, onClose, server, target }: { open: boolean; onClose: () => void; server: Server; target: BackupTarget | undefined }) {
  const qc = useQueryClient()
  const p = policyOf(target)
  const [last, setLast] = useState('')
  const [days, setDays] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (!open) return
    setLast(String(p.keep_last_n || (!p.max_age_days ? DEFAULT_KEEP : '')))
    setDays(p.max_age_days ? String(p.max_age_days) : '')
    // Fill the form once per opening.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])
  const n = last.trim() ? Number(last) : 0
  const d = days.trim() ? Number(days) : 0
  const valid = Number.isInteger(n) && Number.isInteger(d) && n >= 0 && n <= 1000 && d >= 0 && d <= 3650 && (n > 0 || d > 0)
  const save = async () => {
    if (!valid) return
    setBusy(true)
    const retention = { keep_last_n: n, max_age_days: d }
    const r = await act('change how many backups are kept', () =>
      target ? api.backups.targets.update(server.id, target.id, { retention }) : api.backups.targets.create(server.id, { name: 'This node', type: 'local', retention, is_default: true }),
    )
    setBusy(false)
    if (r === undefined) return
    toast('ok', 'Saved how many backups are kept')
    void qc.invalidateQueries({ queryKey: ['backup-targets', server.id] })
    onClose()
  }
  return (
    <Sheet
      open={open}
      onClose={onClose}
      title="How many backups are kept"
      sub={`On ${server.name}’s node, next to the server folder`}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <span className="grow" />
          <Btn variant="primary" disabled={!valid} loading={busy} onClick={() => void save()}>
            Save
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 16 }}>
        <p className="t2">Older backups are deleted after each new one, and the newest is always kept. Leave a box empty for no limit of that kind; at least one is needed.</p>
        <Field label="Keep the most recent" hint="Up to 1000.">
          <div className="row" style={{ gap: 8 }}>
            <input className="input num" inputMode="numeric" style={{ width: 90 }} value={last} onChange={(e) => setLast(e.target.value.replace(/\D/g, ''))} />
            <span className="t2">backups</span>
          </div>
        </Field>
        <Field label="And none older than" hint="Up to 3650.">
          <div className="row" style={{ gap: 8 }}>
            <input className="input num" inputMode="numeric" style={{ width: 90 }} value={days} onChange={(e) => setDays(e.target.value.replace(/\D/g, ''))} />
            <span className="t2">days</span>
          </div>
        </Field>
        {!valid && <p className="small warn">Keep at least one backup, or a number of days.</p>}
      </div>
    </Sheet>
  )
}
