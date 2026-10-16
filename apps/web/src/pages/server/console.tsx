import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '@/lib/api'
import { COMMANDS } from '@/pages/server/commands'
import type { FleetServer, LogProblem, MyServerPermissions, Server } from '@/lib/types'
import { ServerConsole, subscribeServerMetrics, type WsMessage } from '@/lib/ws'
import { useConnection } from '@/live/connection'
import { useAuthStore } from '@/store/auth'
import { useGo } from '@/shell/nav'
import { paths } from '@/shell/paths'
import { Spark } from '@/ui/charts'
import { Btn, Notice, Num, Select, Tabs, TextBtn, type StatusKey } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtBytes, fmtClock, fmtDay, fmtDur, fmtMB, fmtNum, relTime } from '@/ui/format'
import { Icon } from '@/ui/icon'
import { MenuButton, MenuItem } from '@/ui/overlays'
import { deniedTip } from '@/ui/perm'
import { PlayerHead } from '@/ui/pixel'
import { contentLabel } from '@/ui/platforms'
import { toast } from '@/ui/toasts'
import { cx, plural, toMs } from '@/ui/util'
import { allowed, failedStatus, isLiveStatus, useIdentity, useStartupProblem } from './data'
import { FixSheet, applyPlan, quickFix } from './fix'
import { parseLine, parseLines, splitAdvancement, type ConsoleLine } from './lines'

export type ConsoleView = 'live' | 'files' | 'problems'

const MAX_LINES = 2000
const HISTORY_KEY = (id: string) => `mcsm.console.history.${id}`

function loadHistory(id: string): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(HISTORY_KEY(id)) || '[]') as unknown
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string').slice(-50).reverse() : []
  } catch {
    return []
  }
}

function saveHistory(id: string, newestFirst: string[]) {
  try {
    localStorage.setItem(HISTORY_KEY(id), JSON.stringify(newestFirst.slice(0, 50).reverse()))
  } catch {
    // Storage blocked: history lasts for this visit.
  }
}

export function ConsoleTab({
  server,
  live,
  status,
  perms,
  nodeName,
  view,
  setView,
}: {
  server: Server
  live: FleetServer | undefined
  status: StatusKey
  perms: MyServerPermissions | undefined
  nodeName: string
  view: ConsoleView
  setView: (v: ConsoleView) => void
}) {
  const [fixOpen, setFixOpen] = useState(false)
  const failed = failedStatus(status) || !!live?.active_conflict
  const problem = useStartupProblem(server.id, failed).data
  return (
    <div className="console-page">
      <div className="console-main">
        <ServerNotice server={server} status={status} nodeName={nodeName} problem={problem} perms={perms} onWhy={() => setFixOpen(true)} />
        {allowed(perms, 'console') ? (
          <Console server={server} status={status} perms={perms} view={view} setView={setView} />
        ) : (
          <div className="console">
            <div className="console-out" role="log">
              <div className="ln sys">{deniedTip('console')}</div>
            </div>
          </div>
        )}
      </div>
      <ConsoleAside server={server} live={live} status={status} perms={perms} />
      {problem?.problem && <FixSheet open={fixOpen} onClose={() => setFixOpen(false)} server={server} problem={problem} perms={perms} />}
    </div>
  )
}

// ---- what's wrong, above the console ------------------------------------------------------

function ServerNotice({
  server,
  status,
  nodeName,
  problem,
  perms,
  onWhy,
}: {
  server: Server
  status: StatusKey
  nodeName: string
  problem: ReturnType<typeof useStartupProblem>['data']
  perms: MyServerPermissions | undefined
  onWhy: () => void
}) {
  const qc = useQueryClient()
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  const start = () =>
    void act(`start ${server.name}`, () => api.servers.start(server.id)).then((r) => {
      if (r !== undefined) void qc.invalidateQueries({ queryKey: ['fleet'] })
    })
  let notice = null
  if (failedStatus(status) && problem?.problem) {
    const q = quickFix(problem, perms, admin)
    notice = (
      <Notice
        tone="bad"
        actions={
          <>
            {q && (
              <Btn size="sm" disabled={!q.allowed} tip={q.allowed ? undefined : q.why} onClick={() => void applyPlan(server, q.plan, qc)}>
                {q.label}
              </Btn>
            )}
            <TextBtn quiet onClick={onWhy}>
              {problem.plans.length > 1 ? 'Other ways to fix it' : 'Why?'}
            </TextBtn>
          </>
        }
      >
        <b>It didn’t start.</b> {problem.problem.headline}
      </Notice>
    )
  } else if (failedStatus(status)) {
    notice = (
      <Notice
        tone="bad"
        actions={
          <Btn size="sm" need="power.start" onClick={start}>
            Start it again
          </Btn>
        }
      >
        <b>{status === 'crashed' ? 'It crashed.' : 'It didn’t start.'}</b> The last lines below say why.
      </Notice>
    )
  } else if (status === 'unreachable') {
    notice = (
      <Notice tone="warn">
        <b>{nodeName || 'Its node'} is offline,</b> so this server can’t run until it reconnects.
      </Notice>
    )
  }
  return notice ? <div style={{ marginBottom: 12 }}>{notice}</div> : null
}

// ---- the console ----------------------------------------------------------------------------

interface Stream {
  lines: ConsoleLine[]
  /** When the console's own connection dropped, or null while it's up. */
  pausedAt: number | null
  send: (cmd: string) => boolean
  echo: (cmd: string, failed: boolean) => void
}

/** Follows the server's console over its socket, resuming after a drop. */
function useConsoleStream(serverId: string): Stream {
  const [lines, setLines] = useState<ConsoleLine[]>([])
  const [pausedAt, setPausedAt] = useState<number | null>(null)
  const sc = useRef<ServerConsole | null>(null)
  const seq = useRef(0)
  useEffect(() => {
    const c = new ServerConsole(serverId)
    sc.current = c
    let opened = false
    const off = c.on((msg: WsMessage) => {
      if (msg.type === 'reset') {
        setLines([])
      } else if (msg.type === 'line') {
        const d = msg.data as { line: string; stream: 'stdout' | 'stderr'; ts: number }
        setLines((prev) => {
          const next = parseLine(d.line, { id: ++seq.current, ts: d.ts || Date.now(), stderr: d.stream === 'stderr', prev: prev[prev.length - 1] })
          const out = prev.length >= MAX_LINES ? prev.slice(prev.length - MAX_LINES + 1) : prev.slice()
          out.push(next)
          return out
        })
      } else if (msg.type === 'socket') {
        // Paused only once it has been live: a console still connecting for
        // the first time isn't paused, it's loading.
        if ((msg.data as { open: boolean }).open) {
          opened = true
          setPausedAt(null)
        } else if (opened) {
          setPausedAt((p) => p ?? Date.now())
        }
      }
    })
    void c.connect()
    return () => {
      off()
      c.disconnect()
      sc.current = null
    }
  }, [serverId])
  return {
    lines,
    pausedAt,
    send: (cmd) => {
      const c = sc.current
      if (!c || c.readyState !== WebSocket.OPEN) return false
      c.send(cmd)
      return true
    },
    echo: (cmd, failed) =>
      setLines((prev) => {
        const out = prev.slice(-MAX_LINES + 2)
        out.push({ id: ++seq.current, ts: Date.now(), kind: 'cmd', level: 'INFO', text: cmd, failed })
        if (failed) {
          out.push({ id: ++seq.current, ts: Date.now(), kind: 'sys', level: 'WARN', text: 'Not sent. The panel can’t be reached. Press ↑ to try again.' })
        }
        return out
      }),
  }
}

function Line({ l, q }: { l: ConsoleLine; q: string }) {
  const t = l.time ?? fmtClock(l.ts, true)
  const bad = l.level === 'ERROR' || l.level === 'FATAL'
  const cls = l.kind === 'cmd' ? cx('cmd', l.failed && 'failed') : l.kind === 'sys' ? 'sys' : l.level === 'WARN' ? 'warn' : bad ? 'err' : l.kind === 'join' ? 'join' : ''
  const mark = (text: string) => {
    if (!q) return text
    const i = text.toLowerCase().indexOf(q.toLowerCase())
    return i < 0 ? (
      text
    ) : (
      <>
        {text.slice(0, i)}
        <mark>{text.slice(i, i + q.length)}</mark>
        {text.slice(i + q.length)}
      </>
    )
  }
  const adv = l.kind === 'log' || l.kind === 'join' ? splitAdvancement(l.text) : null
  return (
    <div className={cx('ln', cls)}>
      <span className="ts">{t}</span>
      <span className="msg">
        {(l.level === 'WARN' || bad) && l.kind !== 'cmd' && l.kind !== 'sys' && <span className="lvl">{l.level === 'WARN' ? 'warn' : 'error'}</span>}
        {l.kind === 'sys' ? (
          <>MCSM: {mark(l.text)}</>
        ) : l.kind === 'cmd' ? (
          <>&gt; {mark(l.text)}</>
        ) : adv ? (
          <>
            {mark(adv[0])}
            <span className="adv">{adv[1]}</span>
          </>
        ) : (
          mark(l.text)
        )}
      </span>
    </div>
  )
}

function Console({ server, status, perms, view, setView }: { server: Server; status: StatusKey; perms: MyServerPermissions | undefined; view: ConsoleView; setView: (v: ConsoleView) => void }) {
  const stream = useConsoleStream(server.id)
  const conn = useConnection((s) => s.state)
  const [cmd, setCmd] = useState('')
  const [q, setQ] = useState('')
  const [follow, setFollow] = useState(true)
  const [clearedAt, setClearedAt] = useState(0)
  const [hist, setHist] = useState<string[]>(() => loadHistory(server.id))
  const [hi, setHi] = useState(-1)
  const [ac, setAc] = useState(0)
  const [file, setFile] = useState('latest.log')
  const out = useRef<HTMLDivElement>(null)
  const canRead = allowed(perms, 'files.read')
  const logs = useQuery({ queryKey: ['log-files', server.id], queryFn: () => api.logs.list(server.id), enabled: view === 'files' && canRead, staleTime: 30_000 })
  const fileText = useQuery({ queryKey: ['log-file', server.id, file], queryFn: () => api.logs.read(server.id, file, 2000), enabled: view === 'files' && canRead, staleTime: 15_000 })
  const problems = useQuery({
    queryKey: ['log-problems', server.id],
    // A server that has never started has no log yet, which means no problems.
    queryFn: () => api.logs.problems(server.id).catch((e) => (e instanceof ApiError && e.status === 404 ? { file: 'latest.log', problems: [] } : Promise.reject(e))),
    enabled: canRead,
    staleTime: 30_000,
    refetchInterval: view === 'problems' ? 30_000 : false,
  })

  const fileLines = useMemo(() => (fileText.data ? parseLines(fileText.data.lines) : []), [fileText.data])
  const source = view === 'files' ? fileLines : stream.lines.filter((l) => l.id > clearedAt)
  const lines = q ? source.filter((l) => l.text.toLowerCase().includes(q.toLowerCase())) : source

  useEffect(() => {
    if (follow && out.current) out.current.scrollTop = out.current.scrollHeight
  }, [lines.length, follow, view])
  const onScroll = () => {
    const el = out.current
    if (!el) return
    const bottom = el.scrollHeight - el.scrollTop - el.clientHeight < 30
    if (bottom !== follow) setFollow(bottom)
  }

  const word = cmd.replace(/^\//, '')
  const matches = word && !word.includes(' ') ? COMMANDS.filter((c) => c.cmd.startsWith(word.toLowerCase()) && c.cmd !== word.toLowerCase()).slice(0, 6) : []
  const usage = !matches.length && word.includes(' ') ? COMMANDS.find((c) => c.cmd === word.split(' ')[0].toLowerCase())?.hint : undefined
  useEffect(() => setAc(0), [cmd])

  const up = status === 'online'
  const canSend = up && allowed(perms, 'console')
  const send = () => {
    const text = cmd.trim().replace(/^\//, '')
    if (!text) return
    const sent = stream.send(text)
    stream.echo(text, !sent)
    const next = [text, ...hist.filter((x) => x !== text)].slice(0, 50)
    setHist(next)
    saveHistory(server.id, next)
    setHi(-1)
    if (sent) setCmd('')
    setFollow(true)
  }
  const onKey = (e: React.KeyboardEvent) => {
    if (matches.length && (e.key === 'Tab' || (e.key === 'Enter' && matches[ac]))) {
      e.preventDefault()
      setCmd(matches[ac].cmd + ' ')
      return
    }
    if (e.key === 'Enter') {
      e.preventDefault()
      send()
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      if (matches.length) setAc((a) => Math.max(0, a - 1))
      else if (hist.length) {
        const n = Math.min(hist.length - 1, hi + 1)
        setHi(n)
        setCmd(hist[n])
      }
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      if (matches.length) setAc((a) => Math.min(matches.length - 1, a + 1))
      else {
        const n = hi - 1
        setHi(n)
        setCmd(n >= 0 ? hist[n] : '')
      }
    } else if (e.key === 'Escape') setCmd('')
  }

  const problemCount = problems.data?.problems.reduce((n, p) => n + p.count, 0) ?? 0
  const pausedAt = stream.pausedAt ?? (conn !== 'up' ? useConnection.getState().since : null)
  const download = (name: string) => void act(`download ${name}`, () => api.files.download(server.id, 'logs/' + name))

  return (
    <div className="console">
      <div className="console-bar">
        <Tabs
          sub
          value={view}
          onChange={(v) => {
            setView(v)
            setFollow(true)
          }}
          label="Console view"
          items={[
            { value: 'live', label: 'Live' },
            { value: 'files', label: 'Log files', disabled: !canRead, tip: canRead ? undefined : deniedTip('files.read') },
            { value: 'problems', label: 'Problems', count: problemCount || undefined, disabled: !canRead, tip: canRead ? undefined : deniedTip('files.read') },
          ]}
        />
        {view === 'files' && (
          <Select
            size="sm"
            value={file}
            onChange={setFile}
            options={(logs.data ?? [{ name: 'latest.log', size: 0, modified: '', compressed: false }]).map((f) => ({ value: f.name, label: f.name, hint: f.size ? fmtBytes(f.size) : undefined }))}
            style={{ width: 'auto', marginLeft: 6 }}
            aria-label="Log file"
          />
        )}
        <span className="grow" />
        {view !== 'problems' && (
          <div className="search" style={{ width: 180 }}>
            <Icon name="search" size={13} />
            <input className="input sm" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter output" aria-label="Filter output" />
          </div>
        )}
        <MenuButton
          label="Console options"
          items={(close) => (
            <>
              {view === 'live' && (
                <MenuItem close={close} onClick={() => setFollow(!follow)}>
                  {follow ? 'Pause scrolling' : 'Follow new lines'}
                </MenuItem>
              )}
              {view === 'live' && (
                <MenuItem close={close} onClick={() => setClearedAt(stream.lines.length ? stream.lines[stream.lines.length - 1].id : 0)}>
                  Clear the screen
                </MenuItem>
              )}
              <MenuItem close={close} need="files.read" onClick={() => download(view === 'files' ? file : 'latest.log')}>
                Download {view === 'files' ? file : 'latest.log'}
              </MenuItem>
            </>
          )}
        />
      </div>
      {view === 'problems' ? (
        <ProblemList problems={problems.data?.problems} loading={problems.isLoading} serverId={server.id} />
      ) : (
        <div className="console-out" ref={out} onScroll={onScroll} role="log" aria-live="off">
          {lines.map((l) => (
            <Line key={l.id} l={l} q={q} />
          ))}
          {lines.length === 0 && (
            <div className="ln sys">
              {view === 'files' && fileText.isLoading ? 'Loading…' : q ? 'No lines match.' : view === 'files' ? 'This file is empty.' : isLiveStatus(status) ? 'Waiting for output.' : 'Nothing yet. Output shows here once the server starts.'}
            </div>
          )}
          {view === 'files' && fileText.data?.truncated && <div className="ln sys">Showing the last {fmtNum(fileText.data.lines.length)} lines. Download the file for all of it.</div>}
        </div>
      )}
      {view === 'live' && pausedAt != null && (
        <div className="console-paused" role="status">
          Live output paused at {fmtClock(pausedAt, true)}. Anything after that shows up when the connection is back.
        </div>
      )}
      {view === 'live' && !follow && (
        <Btn size="sm" className="jump" onClick={() => setFollow(true)}>
          Jump to the latest
        </Btn>
      )}
      {view === 'live' && (
        <div className="console-in">
          {matches.length > 0 && (
            <div className="suggest" role="listbox">
              {matches.map((c, i) => (
                <button
                  key={c.cmd}
                  type="button"
                  role="option"
                  aria-selected={i === ac}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    setCmd(c.cmd + ' ')
                  }}
                >
                  <span>{c.cmd}</span>
                  <span className="usage">{c.hint}</span>
                </button>
              ))}
            </div>
          )}
          <span className="prompt">/</span>
          <input
            value={cmd}
            onChange={(e) => setCmd(e.target.value)}
            onKeyDown={onKey}
            disabled={!canSend}
            autoFocus
            aria-label="Command"
            placeholder={!allowed(perms, 'console') ? deniedTip('console') : up ? 'Type a command' : status === 'starting' ? 'Waiting for it to finish starting' : 'Start the server to send commands'}
          />
          {usage && <span className="usage">{usage}</span>}
        </div>
      )}
    </div>
  )
}

function ProblemList({ problems, loading, serverId }: { problems: LogProblem[] | undefined; loading: boolean; serverId: string }) {
  const go = useGo()
  const list = useMemo(() => [...(problems ?? [])].sort((a, b) => (toMs(b.last_at) ?? b.last_line) - (toMs(a.last_at) ?? a.last_line)), [problems])
  return (
    <div className="console-out" style={{ fontFamily: 'var(--font)', fontSize: 13.5, padding: '6px 16px' }}>
      {loading && <p className="t3" style={{ padding: '10px 0' }}>Reading latest.log…</p>}
      {!loading && list.length === 0 && (
        <p className="t2" style={{ padding: '10px 0' }}>
          No warnings or errors in latest.log.
        </p>
      )}
      {list.map((g) => {
        const last = toMs(g.last_at)
        return (
          <div key={g.level + g.message} style={{ padding: '10px 0', borderBottom: '1px solid var(--line)' }}>
            <div className="mono small" style={{ color: g.level === 'WARN' ? 'var(--c-warn)' : 'var(--c-err)', wordBreak: 'break-word', whiteSpace: 'pre-wrap' }}>
              {g.sample.split('\n').slice(0, 6).join('\n')}
            </div>
            <div className="t3 small" style={{ marginTop: 3 }}>
              {g.count > 1 ? `${g.count} times` : 'Once'}
              {last ? `, most recently ${relTime(last)}` : ''}
              {/Can't keep up/.test(g.message) && (
                <>
                  .{' '}
                  <TextBtn quiet onClick={() => go(paths.server(serverId, 'stats'))}>
                    Tick health
                  </TextBtn>
                </>
              )}
            </div>
          </div>
        )
      })}
    </div>
  )
}

// ---- beside the console ---------------------------------------------------------------------

interface LiveSample {
  cpu: number | null
  ram: number | null
  tps: number | null
}

/** The server's live CPU, memory and TPS, from its metrics socket. */
function useLiveSample(serverId: string, on: boolean, fallback: FleetServer | undefined): LiveSample {
  const [s, setS] = useState<LiveSample | null>(null)
  useEffect(() => {
    if (!on) {
      setS(null)
      return
    }
    return subscribeServerMetrics(serverId, (msg) => {
      if (msg.type !== 'metrics') return
      const d = msg.data as { cpu_percent?: number; ram_used_mb?: number; tps?: number }
      setS((prev) => ({ cpu: d.cpu_percent ?? prev?.cpu ?? null, ram: d.ram_used_mb ?? prev?.ram ?? null, tps: d.tps ?? prev?.tps ?? null }))
    })
  }, [serverId, on])
  return s ?? { cpu: fallback?.cpu_percent ?? null, ram: fallback?.ram_used_mb ?? null, tps: fallback?.tps ?? null }
}

function ConsoleAside({ server, live, status, perms }: { server: Server; live: FleetServer | undefined; status: StatusKey; perms: MyServerPermissions | undefined }) {
  const go = useGo()
  const qc = useQueryClient()
  const up = status === 'online'
  const running = isLiveStatus(status)
  const identity = useIdentity(server.id).data
  const players = useQuery({ queryKey: ['players', server.id], queryFn: () => api.players.list(server.id), enabled: up && allowed(perms, 'players'), refetchInterval: up ? 30_000 : false })
  const vitals = useQuery({ queryKey: ['vitals', server.id], queryFn: () => api.servers.vitals(server.id), enabled: up, refetchInterval: up ? 10_000 : false, retry: false })
  const history = useQuery({ queryKey: ['metrics-history', server.id, 1], queryFn: () => api.servers.metricsHistory(server.id, 1), enabled: up, staleTime: 60_000 })
  const tasks = useQuery({ queryKey: ['tasks', server.id], queryFn: () => api.tasks.list(server.id), enabled: allowed(perms, 'tasks'), staleTime: 60_000 })
  const backups = useQuery({ queryKey: ['backups', server.id], queryFn: () => api.backups.list(server.id), enabled: allowed(perms, 'backups'), refetchInterval: (q) => (q.state.data?.some((b) => b.status === 'running') ? 3000 : false) })
  const sample = useLiveSample(server.id, running, live)

  const online = (players.data ?? []).filter((p) => p.online).sort((a, b) => Number(!!b.op) - Number(!!a.op) || (toMs(a.joined_at) ?? 0) - (toMs(b.joined_at) ?? 0))
  const count = up ? (live?.players ?? online.length) : 0
  const max = vitals.data?.players?.max ?? identity?.max_players
  const tps = sample.tps ?? vitals.data?.tps?.m1 ?? null
  const mspt = vitals.data?.mspt?.avg
  const tpsHistory = (history.data?.points ?? []).map((p) => p.tps).filter((v): v is number => v != null)
  const updates = live?.mod_updates ?? 0
  const next = (tasks.data ?? [])
    .filter((t) => t.enabled && t.next_run)
    .map((t) => ({ t, at: toMs(t.next_run) as number }))
    .sort((a, b) => a.at - b.at)
    .slice(0, 3)
  const finished = (backups.data ?? []).filter((b) => b.status !== 'running')
  const last = finished[0]
  const backingUp = (backups.data ?? []).some((b) => b.status === 'running')
  const backUp = () =>
    void act(`back up ${server.name}`, () => api.backups.create(server.id)).then((r) => {
      if (r === undefined) return
      toast('info', `Backing up ${server.name}`)
      void qc.invalidateQueries({ queryKey: ['backups', server.id] })
    })

  return (
    <aside className="console-aside">
      <div className="aside-block">
        <div className="aside-title">Playing now</div>
        {up && (
          <div className="big" style={{ marginBottom: 12 }}>
            <Num value={count} />
            {max ? <small>of {max}</small> : null}
          </div>
        )}
        {!up ? (
          <p className="t3 small">Nobody, it’s not running.</p>
        ) : !allowed(perms, 'players') ? null : online.length === 0 ? (
          <p className="t3 small">Nobody right now.</p>
        ) : (
          <>
            {online.slice(0, 12).map((p) => (
              <button key={p.name} type="button" className="who" onClick={() => go(paths.server(server.id, 'players'))}>
                <PlayerHead name={p.bedrock ? '.' + p.name : p.name} size={16} />
                <span className="name ellipsis">{p.name}</span>
                {p.op && <span className="t3 tiny">op</span>}
                <span className="aside">{p.joined_at ? fmtDur(Date.now() - (toMs(p.joined_at) ?? Date.now()), 1) : ''}</span>
              </button>
            ))}
            {online.length > 12 && (
              <TextBtn quiet className="small" onClick={() => go(paths.server(server.id, 'players'))}>
                and {online.length - 12} more
              </TextBtn>
            )}
          </>
        )}
      </div>
      <div className="aside-block">
        <div className="aside-title">Health</div>
        {running ? (
          <>
            {up && tps != null && (
              <div className={cx('big', tps < 15 ? 'bad' : tps < 18.5 ? 'warn' : '')} style={{ marginBottom: 4 }}>
                <Num value={tps} format={(v) => v.toFixed(1)} />
                <small>ticks per second</small>
              </div>
            )}
            {up && tpsHistory.length > 1 && (
              <div style={{ margin: '12px 0 12px' }} data-tip="Ticks per second, last hour">
                <Spark fluid values={tpsHistory} w={240} h={24} min={10} max={20.5} />
              </div>
            )}
            {up && mspt != null && (
              <div className="kv">
                <span>Time per tick</span>
                <span>
                  <Num value={mspt} format={(v) => Math.round(v) + ' ms'} />
                </span>
              </div>
            )}
            <div className="kv">
              <span>CPU</span>
              <span>{sample.cpu != null ? <Num value={sample.cpu} format={(v) => Math.round(v) + '%'} /> : '–'}</span>
            </div>
            <div className="kv">
              <span>Memory</span>
              <span>
                {sample.ram != null ? <Num value={sample.ram} format={(v) => fmtMB(v, 1)} /> : '–'} <span className="t3">of {fmtMB(server.ram_mb_max)}</span>
              </span>
            </div>
            {up && vitals.data?.entities != null && (
              <div className="kv">
                <span>Entities</span>
                <span>
                  <Num value={vitals.data.entities} format={fmtNum} />
                </span>
              </div>
            )}
          </>
        ) : (
          <p className="t3 small">Not running.</p>
        )}
      </div>
      {updates > 0 && allowed(perms, 'mods') && (
        <div className="aside-block">
          <div className="aside-title">{contentLabel(server.platform)}</div>
          <p className="small t2">{plural(updates, 'update')} waiting.</p>
          <div style={{ marginTop: 8 }}>
            <Btn size="sm" onClick={() => go(paths.server(server.id, 'mods'))}>
              See updates
            </Btn>
          </div>
        </div>
      )}
      {allowed(perms, 'tasks') && (
        <div className="aside-block">
          <div className="aside-title">Coming up</div>
          {next.length ? (
            next.map(({ t, at }) => (
              <div key={t.id} className="kv">
                <span className="ellipsis">{t.name}</span>
                <span className="num t3">{fmtDay(at) === 'Today' ? fmtClock(at) : fmtDay(at).slice(0, 3) + ' ' + fmtClock(at)}</span>
              </div>
            ))
          ) : (
            <p className="t3 small">Nothing scheduled.</p>
          )}
        </div>
      )}
      {allowed(perms, 'backups') && (
        <div className="aside-block">
          <div className="aside-title">Last backup</div>
          {last ? (
            last.status === 'success' ? (
              <p className="small t2">
                {fmtDay(toMs(last.started_at) ?? 0)} at {fmtClock(toMs(last.started_at) ?? 0)}
                {last.size_bytes != null ? `, ${fmtBytes(last.size_bytes)}` : ''}.
              </p>
            ) : (
              <p className="small bad">Failed {relTime(toMs(last.started_at))}.</p>
            )
          ) : (
            <p className="small t3">There isn’t one yet.</p>
          )}
          <div style={{ marginTop: 8 }}>
            <Btn size="sm" need="backups.create" loading={backingUp} onClick={backUp}>
              Back up now
            </Btn>
          </div>
        </div>
      )}
    </aside>
  )
}
