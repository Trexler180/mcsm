import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { ScheduledTask, Server, ServerTime, TaskRun } from '@/lib/types'
import { Btn, Check, Empty, Field, Select, Switch, TextBtn, TimeField } from '@/ui/controls'
import { cronOf, describeCron, fiveMinutesBefore, nextRun, nextRuns, parseCron, relLabel, whenOf, type When } from '@/ui/cron'
import { act } from '@/ui/failure'
import { DOW_FULL, fmtClock, fmtDay, fmtDur, relTime } from '@/ui/format'
import { Dialog, MenuButton, MenuItem, MenuSep } from '@/ui/overlays'
import { Loaded, SkItems } from '@/ui/skeleton'
import { ask } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { clamp, cx, pad2, plural, toMs } from '@/ui/util'

// Schedules: what a server does on its own. Cron runs on the panel's clock,
// so times here are in the panel's time zone.

const ACTIONS: Record<string, string> = {
  command: 'Run a command',
  restart: 'Restart the server',
  stop: 'Stop the server',
  start: 'Start the server',
  backup: 'Back it up',
  mod_update: 'Update mods safely',
}

const WHEN: { value: When; label: string }[] = [
  { value: 'hourly', label: 'Every hour' },
  { value: 'daily', label: 'Every day' },
  { value: 'weekly', label: 'Every week' },
  { value: 'monthly', label: 'Every month' },
  { value: 'custom', label: 'A cron expression' },
]

/** The name a schedule gets when it's left blank: what it does and how often,
 *  like "Nightly backup". */
export function suggestedName(action: string, when: When): string {
  const what: Record<string, string> = { command: 'command', restart: 'restart', stop: 'stop', start: 'start', backup: 'backup', mod_update: 'mod update' }
  const often: Record<When, string> = { hourly: 'Hourly', daily: 'Nightly', weekly: 'Weekly', monthly: 'Monthly', custom: 'Scheduled' }
  return `${often[when]} ${what[action] ?? action}`
}

/** The panel's clock against this browser's: schedule times are shown on the
 *  panel's, which is where cron fires. */
interface Clock {
  /** Panel wall time minus browser wall time, in ms. */
  delta: number
  zone: string
  same: boolean
}

function clockOf(t: ServerTime | undefined): Clock {
  const browser = -new Date().getTimezoneOffset() * 60
  if (!t) return { delta: 0, zone: Intl.DateTimeFormat().resolvedOptions().timeZone?.replace(/_/g, ' ') || 'local time', same: true }
  return { delta: (t.offset_seconds - browser) * 1000, zone: t.zone, same: t.offset_seconds === browser }
}

/** When an expression runs next, as a real moment, evaluated on the panel's clock. */
const nextOn = (c: Clock, expr: string, from = Date.now()) => {
  const n = nextRun(expr, from + c.delta)
  return n == null ? null : n - c.delta
}
/** A moment as the panel's wall clock reads it. */
const onPanel = (c: Clock, t: number) => t + c.delta

function taskSentence(t: ScheduledTask): string {
  const when = describeCron(t.cron_expr).replace(/^Custom: /, 'On the schedule ')
  const cmd = (t.payload?.command as string | undefined) ?? ''
  const what = t.action === 'command' ? (cmd ? `run /${cmd}` : 'run a command') : (ACTIONS[t.action] ?? t.action).toLowerCase()
  return `${when}, ${what}.`
}

export function SchedulesTab({ server }: { server: Server }) {
  const tasks = useQuery({ queryKey: ['tasks', server.id], queryFn: () => api.tasks.list(server.id) })
  const time = useQuery({ queryKey: ['panel-time'], queryFn: () => api.time(), staleTime: 10 * 60_000, retry: false }).data
  return (
    <Loaded query={tasks} sk={<SkItems rows={4} />} what={`schedules for ${server.name}`}>
      {(list) => <SchedulesBody server={server} list={list} clock={clockOf(time)} />}
    </Loaded>
  )
}

function SchedulesBody({ server, list, clock }: { server: Server; list: ScheduledTask[]; clock: Clock }) {
  const qc = useQueryClient()
  const [edit, setEdit] = useState<Partial<ScheduledTask> | null>(null)
  const [open, setOpen] = useState<Record<string, boolean>>({})
  const refresh = () => void qc.invalidateQueries({ queryKey: ['tasks', server.id] })
  const toggle = (t: ScheduledTask, enabled: boolean) =>
    void act(enabled ? `resume “${t.name}”` : `pause “${t.name}”`, () => api.tasks.update(server.id, t.id, { ...t, enabled })).then((r) => {
      if (r === undefined) return
      toast('ok', enabled ? `Resumed “${t.name}”` : `Paused “${t.name}”`)
      refresh()
    })
  const runNow = (t: ScheduledTask) =>
    void act(`run “${t.name}”`, () => api.tasks.run(server.id, t.id)).then((r) => {
      if (r === undefined) return
      toast('info', `Running “${t.name}”`)
      void qc.invalidateQueries({ queryKey: ['task-runs', server.id, t.id] })
    })
  const remove = async (t: ScheduledTask) => {
    if (!(await ask({ title: `Delete “${t.name}”?`, body: 'Its history goes with it.', confirm: 'Delete', danger: true }))) return
    const r = await act(`delete “${t.name}”`, () => api.tasks.delete(server.id, t.id).then(() => true))
    if (r === undefined) return
    toast('ok', `Deleted “${t.name}”`)
    refresh()
  }
  return (
    <>
      <div className="toolbar" style={{ marginBottom: 20 }}>
        <p className="t2 grow">
          Things {server.name} does on its own. Times are on the panel’s clock, {clock.zone}
          {clock.same ? ', the same as yours' : ''}.
        </p>
        <Btn variant="primary" need="tasks" onClick={() => setEdit({})}>
          New schedule
        </Btn>
      </div>
      {list.length === 0 ? (
        <Empty
          title="Nothing is scheduled."
          action={
            <Btn need="tasks" onClick={() => setEdit({ name: 'Nightly restart', action: 'restart', cron_expr: '0 5 * * *' })}>
              Restart it every night
            </Btn>
          }
        >
          Most servers restart and back up once a night while nobody’s on.
        </Empty>
      ) : (
        <div className="items">
          {list.map((t) => (
            <TaskItem
              key={t.id}
              server={server}
              t={t}
              clock={clock}
              historyOpen={!!open[t.id]}
              toggleHistory={() => setOpen({ ...open, [t.id]: !open[t.id] })}
              onToggle={(v) => toggle(t, v)}
              onRun={() => runNow(t)}
              onEdit={() => setEdit(t)}
              onDuplicate={() => setEdit({ ...t, id: undefined, name: t.name + ' copy' })}
              onDelete={() => void remove(t)}
            />
          ))}
        </div>
      )}
      <TaskDialog server={server} task={edit} clock={clock} onClose={() => setEdit(null)} />
    </>
  )
}

const RUN_WORD: Record<TaskRun['status'], string> = { running: 'Running', succeeded: 'Done', failed: 'Failed', skipped: 'Skipped' }

function TaskItem({
  server,
  t,
  clock,
  historyOpen,
  toggleHistory,
  onToggle,
  onRun,
  onEdit,
  onDuplicate,
  onDelete,
}: {
  server: Server
  t: ScheduledTask
  clock: Clock
  historyOpen: boolean
  toggleHistory: () => void
  onToggle: (v: boolean) => void
  onRun: () => void
  onEdit: () => void
  onDuplicate: () => void
  onDelete: () => void
}) {
  const runs = useQuery({ queryKey: ['task-runs', server.id, t.id], queryFn: () => api.tasks.runs(server.id, t.id, 12), staleTime: 30_000, retry: false }).data ?? []
  const nx = t.enabled ? (toMs(t.next_run) ?? nextOn(clock, t.cron_expr)) : null
  const last = runs[0]
  const lastAt = toMs(last?.started_at) ?? toMs(t.last_run)
  const failedLast = last?.status === 'failed'
  const day = (at: number) => {
    const d = fmtDay(onPanel(clock, at))
    return d === 'Today' ? 'today' : d === 'Tomorrow' ? 'tomorrow' : 'on ' + d
  }
  return (
    <div className="item" style={{ alignItems: 'flex-start', opacity: t.enabled ? 1 : 0.6 }}>
      <div className="grow" style={{ minWidth: 0 }}>
        <div className="title">
          {t.name}
          {!t.enabled && (
            <span className="t3" style={{ fontWeight: 400 }}>
              {' '}
              (paused)
            </span>
          )}
        </div>
        <div className="t2 small">{taskSentence(t)}</div>
        <div className="meta">
          {nx ? `Next ${day(nx)} at ${fmtClock(onPanel(clock, nx))}.` : 'Paused.'}
          {lastAt && !failedLast ? ` Last ran ${relTime(lastAt)}${last?.status === 'skipped' ? `, skipped${last.message ? ': ' + last.message : ''}` : ''}.` : ''}
          {failedLast && (
            <span className="bad">
              {' '}
              Failed {relTime(lastAt)}
              {last.message ? ': ' + last.message.charAt(0).toLowerCase() + last.message.slice(1).replace(/\.$/, '') : ''}.
            </span>
          )}
        </div>
        <div className="row" style={{ gap: 14, alignItems: 'center', marginTop: 8 }}>
          <RunTimeline t={t} runs={runs} clock={clock} />
          {runs.length > 0 && (
            <TextBtn quiet className="small" onClick={toggleHistory}>
              {historyOpen ? 'Hide history' : 'History'}
            </TextBtn>
          )}
        </div>
        {historyOpen && <RunHistory runs={runs} clock={clock} />}
      </div>
      <TextBtn quiet need="tasks" onClick={onRun}>
        Run now
      </TextBtn>
      <Switch checked={t.enabled} label={`${t.name} on`} need="tasks" onChange={onToggle} />
      <MenuButton
        label={`More for ${t.name}`}
        items={(close) => (
          <>
            <MenuItem close={close} need="tasks" onClick={onEdit}>
              Edit
            </MenuItem>
            <MenuItem close={close} need="tasks" onClick={onDuplicate}>
              Duplicate
            </MenuItem>
            <MenuSep />
            <MenuItem danger close={close} need="tasks" onClick={onDelete}>
              Delete
            </MenuItem>
          </>
        )}
      />
    </div>
  )
}

/** Past runs and the next few on one line, placed by time, with now marked. */
function RunTimeline({ t, runs, clock }: { t: ScheduledTask; runs: TaskRun[]; clock: Clock }) {
  const past = runs.slice(0, 12).reverse()
  const next: number[] = []
  if (t.enabled) {
    let x: number | null = Date.now()
    for (let i = 0; i < 3; i++) {
      x = x == null ? null : nextOn(clock, t.cron_expr, x)
      if (x == null) break
      next.push(x)
    }
  }
  if (!past.length && !next.length) return null
  const now = Date.now()
  const from = past.length ? (toMs(past[0].started_at) ?? now) : now
  const to = next.length ? next[next.length - 1] : now
  const X = (ts: number) => ((ts - from) / Math.max(1, to - from)) * 100
  const failed = past.filter((r) => r.status === 'failed').length
  const label = (ts: number) => `${fmtDay(onPanel(clock, ts))} ${fmtClock(onPanel(clock, ts))}`
  return (
    <div className="run-line" role="img" aria-label={`${plural(past.length, 'recent run')}${failed ? `, ${failed} failed` : ''}.${next.length ? ' Next ' + label(next[0]) : ''}`}>
      <div className="track">
        {past.map((r) => {
          const at = toMs(r.started_at) ?? now
          const cls = r.status === 'succeeded' ? 'success' : r.status
          return <span key={r.id} className={cx('run', cls)} style={{ left: X(at) + '%' }} data-tip={`${label(at)}. ${RUN_WORD[r.status]}${r.message ? '. ' + r.message : ''}`} />
        })}
        <span className="now" style={{ left: X(now) + '%' }} data-tip="Now" />
        {next.map((ts, i) => (
          <span key={'n' + i} className="run next" style={{ left: X(ts) + '%' }} data-tip={`Next: ${label(ts)}`} />
        ))}
      </div>
      <div className="ends t3 tiny num">
        <span>{relLabel(from)}</span>
        <span>{relLabel(to)}</span>
      </div>
    </div>
  )
}

function RunHistory({ runs, clock }: { runs: TaskRun[]; clock: Clock }) {
  return (
    <table className="t run-history">
      <thead>
        <tr>
          <th>When</th>
          <th>Result</th>
          <th className="r">Took</th>
        </tr>
      </thead>
      <tbody>
        {runs.map((r) => {
          const a = toMs(r.started_at) ?? 0
          const b = toMs(r.finished_at)
          return (
            <tr key={r.id}>
              <td className="nowrap">
                {fmtDay(onPanel(clock, a))} {fmtClock(onPanel(clock, a))}
              </td>
              <td className={r.status === 'failed' ? 'bad' : r.status === 'skipped' ? 't3' : 't2'}>
                {RUN_WORD[r.status]}
                {r.message ? `. ${r.message}` : ''}
              </td>
              <td className="r num t2">{b ? fmtDur(Math.max(1000, b - a), 2) : '–'}</td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

function TaskDialog({ server, task, clock, onClose }: { server: Server; task: Partial<ScheduledTask> | null; clock: Clock; onClose: () => void }) {
  const qc = useQueryClient()
  const editing = !!task?.id
  const [name, setName] = useState('')
  const [action, setAction] = useState('restart')
  const [command, setCommand] = useState('')
  const [when, setWhen] = useState<When>('daily')
  const [time, setTime] = useState('05:00')
  const [dow, setDow] = useState('1')
  const [dom, setDom] = useState('1')
  const [cron, setCron] = useState('0 5 * * *')
  const [warn, setWarn] = useState(true)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (!task) return
    const c = task.cron_expr || '0 5 * * *'
    const shape = whenOf(c)
    setName(task.name || '')
    setAction(task.action || 'restart')
    setCommand((task.payload?.command as string | undefined) ?? '')
    setCron(c)
    setWhen(shape.when)
    setTime(shape.time)
    setDow(shape.dow)
    setDom(shape.dom)
    setWarn(!editing)
  }, [task, editing])
  if (!task) return null
  const [hh, mm] = time.split(':').map((x) => +x)
  const expr = cronOf(when, time, dow, dom, cron)
  const okCron = !!parseCron(expr)
  // A blank name is fine: the schedule takes the suggested one shown in the field.
  const finalName = name.trim() || suggestedName(action, when)
  const valid = okCron && (action !== 'command' || !!command.trim())
  const runs = okCron ? nextRuns(expr, 3, Date.now() + clock.delta).map((t) => t - clock.delta) : []
  const save = async () => {
    if (!valid) return
    setBusy(true)
    const body: Partial<ScheduledTask> = {
      name: finalName,
      action,
      cron_expr: expr,
      payload: action === 'command' ? { command: command.trim().replace(/^\//, '') } : null,
      enabled: task.enabled ?? true,
    }
    const r = await act(editing ? `save “${finalName}”` : `schedule “${finalName}”`, async () => {
      const saved = editing ? await api.tasks.update(server.id, task.id as string, body) : await api.tasks.create(server.id, body)
      const before = !editing && warn && (action === 'restart' || action === 'stop') ? fiveMinutesBefore(expr) : null
      if (before) {
        await api.tasks.create(server.id, {
          name: `${finalName} warning`,
          action: 'command',
          cron_expr: before,
          payload: { command: `say ${action === 'restart' ? 'Restarting' : 'Shutting down'} in 5 minutes` },
          enabled: true,
        })
      }
      return saved
    })
    setBusy(false)
    if (r === undefined) return
    toast('ok', editing ? `Saved “${finalName}”` : `Scheduled “${finalName}”`)
    void qc.invalidateQueries({ queryKey: ['tasks', server.id] })
    onClose()
  }
  return (
    <Dialog
      open
      onClose={onClose}
      title={editing ? `Edit “${task.name}”` : 'New schedule'}
      size="wide"
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" disabled={!valid} loading={busy} onClick={() => void save()}>
            {editing ? 'Save' : 'Schedule it'}
          </Btn>
        </>
      }
    >
      <div className="form-grid">
        <Field label="Name" className="span">
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder={suggestedName(action, when)} aria-label="Name" autoFocus />
        </Field>
        <Field label="What to do">
          <Select value={action} onChange={setAction} options={Object.entries(ACTIONS).map(([value, label]) => ({ value, label }))} aria-label="What to do" />
        </Field>
        <Field label="When">
          <Select value={when} onChange={setWhen} options={WHEN} aria-label="When" />
        </Field>
        {action === 'command' && (
          <Field label="Command" className="span">
            <div className="affix">
              <span className="mono">/</span>
              <input className="input mono" value={command} onChange={(e) => setCommand(e.target.value)} placeholder="say The event starts in 10 minutes" />
            </div>
          </Field>
        )}
        {when === 'weekly' && (
          <Field label="On">
            <Select value={dow} onChange={setDow} options={DOW_FULL.map((d, i) => ({ value: String(i), label: d }))} aria-label="On" />
          </Field>
        )}
        {when === 'monthly' && (
          <Field label="On the">
            <Select value={dom} onChange={setDom} options={Array.from({ length: 28 }, (_, i) => ({ value: String(i + 1), label: String(i + 1) }))} aria-label="On the" />
          </Field>
        )}
        {(when === 'daily' || when === 'weekly' || when === 'monthly') && (
          <Field label="At" hint={`24-hour, on the panel’s clock (${clock.zone}). Arrow keys step 5 minutes.`}>
            <TimeField value={time} onChange={setTime} label="At" />
          </Field>
        )}
        {when === 'hourly' && (
          <Field label="Minutes past the hour">
            <input className="input num" inputMode="numeric" value={mm} onChange={(e) => setTime(pad2(hh || 0) + ':' + pad2(clamp(+e.target.value.replace(/\D/g, '') || 0, 0, 59)))} />
          </Field>
        )}
        {when === 'custom' && (
          <Field label="Cron expression" hint="minute hour day month weekday" error={!okCron ? 'That isn’t a valid cron expression.' : null}>
            <input className="input mono" value={cron} onChange={(e) => setCron(e.target.value)} />
          </Field>
        )}
        {!editing && (action === 'restart' || action === 'stop') && (
          <div className="span">
            <Check checked={warn} onChange={setWarn} label="Warn players in chat five minutes before" />
          </div>
        )}
        <div className="span t2 small">
          {okCron && runs.length ? <>Next runs: {runs.map((t) => `${fmtDay(onPanel(clock, t))} at ${fmtClock(onPanel(clock, t))}`).join(', ')}.</> : okCron ? 'It never runs.' : 'Fix the schedule to see when it runs.'}
        </div>
      </div>
    </Dialog>
  )
}
