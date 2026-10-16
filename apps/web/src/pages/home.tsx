import { useMemo, useState, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useMcpApprovalDialog } from '@/shell/agent-review'
import { api } from '@/lib/api'
import { asJoinDeniedAlert, whitelistFromAlert } from '@/lib/join-denied'
import { actionPhrase } from '@/lib/mcp-action-alert'
import type { AuditEntryWithActor, FleetNode, FleetServer, FleetSummary, MCPActionRequest, MyServerPermissions, NotificationItem } from '@/lib/types'
import { useAuthStore } from '@/store/auth'
import { useNotificationFeed } from '@/store/notification-feed'
import { useTicker } from '@/shell/banners'
import { effStatus, groupFleet, useFleet, useFolders } from '@/shell/fleet'
import { useGo } from '@/shell/nav'
import { auditSentence } from './audit-text'
import { paths } from '@/shell/paths'
import { useServerPerms } from '@/shell/perms'
import { Btn, Empty, Filters, Meter, Num, STATE, Status, type StatusKey } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtClock, fmtDur, fmtMB, relTime } from '@/ui/format'
import { Icon, type IconName } from '@/ui/icon'
import { PermScope } from '@/ui/perm'
import { ServerIcon } from '@/ui/pixel'
import { platformLabel } from '@/ui/platforms'
import { HomeSkeleton, Loaded } from '@/ui/skeleton'
import { toast } from '@/ui/toasts'
import { DAY, cap, cx, plural, toMs } from '@/ui/util'
import type { ServerPermission } from '@/lib/types'

// Home: who's playing, what needs attention, every server on one line each,
// and the nodes, schedules and activity around them. One request (/fleet),
// kept current by the live event stream.

const isLive = (s: FleetServer) => s.status === 'online' || s.status === 'starting'
const isBusy = (s: FleetServer) => s.status === 'starting' || s.status === 'stopping'
const failedStart = (s: FleetServer) => s.status === 'crashed' || s.status === 'startup_failure'
const uptime = (s: FleetServer, now: number) => (s.online_since ? fmtDur(now - s.online_since * 1000, 1) : '')
const played = (s: FleetServer) => s.players_24h.some((v) => (v ?? 0) > 0) || (s.players ?? 0) > 0
/** A server whose backups are worth worrying about: failing, or out of date
 *  on a server that has had backups or players. A new, empty server that was
 *  never backed up isn't one. */
const backupFailing = (s: FleetServer) => !!s.backup_failed_at
const backupLate = (s: FleetServer) => !s.backup_failed_at && s.backup_overdue && (!!s.last_backup_at || played(s))

/** "9 days", "5 hours": how long since, for a sentence. */
function howLong(ms: number): string {
  const days = Math.floor(ms / DAY)
  if (days >= 1) return plural(days, 'day')
  return plural(Math.max(1, Math.floor(ms / (60 * 60 * 1000))), 'hour')
}

export function HomePage() {
  const fleet = useFleet()
  return (
    <div className="scroll">
      <Loaded query={fleet} sk={<HomeSkeleton />} what="Home" page>
        {(f) => <Home fleet={f} />}
      </Loaded>
    </div>
  )
}

interface Issue {
  key: string
  tone: 'bad' | 'warn'
  icon?: IconName
  lead: string
  rest?: string
  serverId?: string
  acts: { label: string; run: () => void; primary?: boolean; text?: boolean; need?: ServerPermission }[]
}

function Home({ fleet }: { fleet: FleetSummary }) {
  useTicker(true, 30_000)
  const now = Date.now()
  const go = useGo()
  const qc = useQueryClient()
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  const folders = useFolders().data
  const permsFor = useServerPerms()
  const feed = useNotificationFeed()
  const requests = useQuery({ queryKey: ['mcp-action-requests'], queryFn: () => api.mcp.actionRequests.list(), staleTime: 15_000 }).data
  const [show, setShow] = useState<'all' | 'up' | 'down' | 'bad'>('all')

  const servers = useMemo(() => groupFleet(fleet.servers, folders ?? []).flatMap((g) => g.servers), [fleet.servers, folders])
  const nodes = fleet.nodes
  const es = (s: FleetServer) => effStatus(s, nodes)
  const refresh = () => void qc.invalidateQueries({ queryKey: ['fleet'] })

  const start = (s: FleetServer) => void act(`start ${s.name}`, () => api.servers.start(s.id))
  const restart = (s: FleetServer) => void act(`restart ${s.name}`, () => api.servers.restart(s.id))

  const issues = homeIssues({ fleet, servers, requests, feed: feed.items, now, go, start, refresh, markRead: feed.markRead })

  const running = servers.filter((s) => s.status === 'online')
  const players = fleet.figures.players_online
  const busyServers = running.filter((s) => (s.players ?? 0) > 0)
  const totals = Array.from({ length: 24 }, (_, i) => servers.reduce((sum, s) => sum + (s.players_24h[i] ?? 0), 0))
  totals[23] = Math.max(totals[23], players)
  const peak = Math.max(...totals)
  const busiest = [...running].sort((a, b) => (b.players ?? 0) - (a.players ?? 0))[0]
  const nodesUp = nodes.filter((n) => n.online)
  const avgCpu = nodesUp.length ? Math.round(nodesUp.reduce((s, n) => s + (n.cpu_pct ?? 0), 0) / nodesUp.length) : 0
  const unread = feed.items.filter((n) => !n.read_at)
  const unreadCount = feed.unread || fleet.figures.alerts_unread
  const failing = servers.filter(backupFailing)
  const late = servers.filter(backupLate)
  const lastBackup = Math.max(0, ...servers.map((s) => toMs(s.last_backup_at) ?? 0))
  const upd = servers.filter((s) => (s.mod_updates ?? 0) > 0)

  const counts = {
    up: servers.filter(isLive).length,
    down: servers.filter((s) => !isLive(s) && !failedStart(s) && es(s) !== 'unreachable').length,
    bad: servers.filter((s) => failedStart(s) || es(s) === 'unreachable').length,
  }
  const list = servers.filter((s) =>
    show === 'all' ? true : show === 'up' ? isLive(s) : show === 'down' ? !isLive(s) && !failedStart(s) && es(s) !== 'unreachable' : failedStart(s) || es(s) === 'unreachable',
  )
  const nodeName = (id: string) => nodes.find((n) => n.id === id)?.name ?? 'Unknown node'

  return (
    <div className="page home">
      <header className="home-head">
        <h1 className="home-title">
          {players ? (
            <>
              {players} {players === 1 ? 'person' : 'people'} playing on {plural(busyServers.length, 'server')}
            </>
          ) : running.length ? (
            'Running, nobody on'
          ) : servers.length ? (
            'Nothing running'
          ) : (
            'No servers yet'
          )}
        </h1>
        <span className="t3 small">{issues.length ? plural(issues.length, 'thing needs', 'things need') + ' attention' : 'All clear'}</span>
        <span className="grow" />
        {admin && (
          <Btn size="sm" icon="plus" onClick={() => go(paths.newServer)}>
            Add a server
          </Btn>
        )}
      </header>

      <section className="kpis">
        <Kpi
          label="Playing now"
          value={<Num value={players} />}
          onClick={busiest && (busiest.players ?? 0) > 0 ? () => go(paths.server(busiest.id, 'players')) : undefined}
          aside={<span className="t3 small">peak {peak} today</span>}
        >
          <span className="bars" data-tip="Everyone online, last 24 hours">
            {hourBars(totals, Math.max(1, peak), true)}
          </span>
        </Kpi>
        <Kpi
          label="Servers up"
          value={
            <>
              <Num value={running.length} />
              <span className="t3">/{servers.length}</span>
            </>
          }
        >
          <span className="segs">
            {servers.map((s) => {
              const st = es(s)
              const [label, tone] = STATE[st]
              return <button key={s.id} type="button" className={cx('seg', st === 'online' ? 'up' : tone)} onClick={() => go(paths.server(s.id))} data-tip={`${s.name}: ${label}`} aria-label={`${s.name}: ${label}`} />
            })}
          </span>
        </Kpi>
        <Kpi
          label="Nodes"
          value={
            <>
              <Num value={nodesUp.length} />
              <span className="t3">/{nodes.length}</span>
            </>
          }
          tone={nodesUp.length < nodes.length ? 'warn' : ''}
          onClick={admin ? () => go(paths.nodes) : undefined}
        >
          <span className="small t3">
            {nodesUp.length < nodes.length ? (
              <span className="bad">
                {nodes
                  .filter((n) => !n.online)
                  .map((n) => n.name)
                  .join(', ')}{' '}
                offline
              </span>
            ) : nodes.length ? (
              `avg CPU ${avgCpu}%`
            ) : (
              'None yet'
            )}
          </span>
        </Kpi>
        <Kpi label="Unread alerts" value={<Num value={unreadCount} />} tone={unreadCount ? 'warn' : ''} onClick={() => go(paths.alerts)}>
          <span className="small t3 ellipsis">{unread[0] ? unread[0].title : 'Nothing new'}</span>
        </Kpi>
        <Kpi
          label="Backups"
          value={failing.length ? `${failing.length} failing` : late.length ? `${late.length} overdue` : 'OK'}
          tone={failing.length ? 'bad' : late.length ? 'warn' : ''}
          onClick={failing.length || late.length ? () => go(paths.server((failing[0] ?? late[0]).id, 'backups')) : undefined}
        >
          <span className="small t3 ellipsis">
            {failing.length || late.length ? [...failing, ...late].map((s) => s.name).join(', ') : lastBackup ? 'last ' + relTime(lastBackup, now) : 'none yet'}
          </span>
        </Kpi>
        <Kpi label="Mod updates" value={fleet.figures.mod_updates} onClick={upd.length ? () => go(paths.server(upd[0].id, 'mods')) : undefined}>
          <span className="small t3 ellipsis">{upd.length ? upd.map((s) => `${s.name} ${s.mod_updates}`).join(', ') : 'Everything current'}</span>
        </Kpi>
      </section>

      {issues.length > 0 && (
        <section className="section">
          <div className="section-head">
            <h2 className="section-title">Needs attention</h2>
            <span className="t3 small">{issues.length}</span>
          </div>
          <IssueList items={issues} permsFor={permsFor} />
        </section>
      )}

      <section className="section">
        <div className="section-head">
          <h2 className="section-title">Servers</h2>
          <span className="grow" />
          <Filters
            value={show}
            onChange={setShow}
            label="Show"
            options={[
              { value: 'all', label: 'All', count: servers.length },
              { value: 'up', label: 'Running', count: counts.up },
              { value: 'down', label: 'Stopped', count: counts.down },
              { value: 'bad', label: 'Problems', count: counts.bad || undefined },
            ]}
          />
        </div>
        {!servers.length ? (
          <Empty
            title={admin ? 'No servers yet.' : 'No servers have been shared with you yet.'}
            action={
              admin ? (
                <Btn variant="primary" icon="plus" onClick={() => go(paths.newServer)}>
                  Add a server
                </Btn>
              ) : undefined
            }
          >
            {admin ? 'Add one to start running it from here.' : 'Ask an administrator to give you access to one.'}
          </Empty>
        ) : !list.length ? (
          <Empty title="No servers here." />
        ) : (
          <div className="srv-list">
            <div className="srv-row head" aria-hidden="true">
              <span>Server</span>
              <span>State</span>
              <span className="r">Players</span>
              <span className="r">TPS</span>
              <span className="r">CPU</span>
              <span>Memory</span>
              <span>Last 24 h</span>
              <span>Node</span>
              <span>Backup</span>
            </div>
            {list.map((s) => (
              <PermScope key={s.id} perms={permsFor(s.id)}>
                <ServerRow s={s} status={es(s)} node={nodeName(s.node_id)} now={now} onOpen={() => go(paths.server(s.id))} onStart={() => start(s)} onRestart={() => restart(s)} />
              </PermScope>
            ))}
          </div>
        )}
      </section>

      <div className="section widgets">
        <NodesWidget nodes={nodes} onOpen={admin ? () => go(paths.nodes) : undefined} />
        <section className="tile widget">
          <div className="tile-head">
            <h2 className="tile-title">Coming up</h2>
            <span className="aside">next 24 h</span>
          </div>
          {fleet.upcoming.length ? (
            <div className="mini-t">
              {fleet.upcoming.slice(0, 6).map((u) => (
                <div key={u.task_id} className="mini-row click pair" onClick={() => go(paths.server(u.server_id, 'schedules'))}>
                  <span className="num t2">{fmtClock(toMs(u.next_run) ?? now)}</span>
                  <span className="ellipsis">
                    <span className="strong">{u.name}</span> <span className="t3">{u.server_name}</span>
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <p className="t3 small">Nothing scheduled.</p>
          )}
        </section>
        <section className="tile widget">
          <div className="tile-head">
            <h2 className="tile-title">Recent activity</h2>
            {admin && (
              <Btn size="sm" variant="quiet" onClick={() => go(paths.audit)} style={{ marginLeft: 'auto' }}>
                Audit log
              </Btn>
            )}
          </div>
          <ActivityMini entries={fleet.activity} />
        </section>
      </div>
    </div>
  )
}

// ---- what needs attention ------------------------------------------------------------------

export function homeIssues(o: {
  fleet: FleetSummary
  servers: FleetServer[]
  requests: MCPActionRequest[] | undefined
  feed: NotificationItem[]
  now: number
  go: (to: string) => void
  start: (s: FleetServer) => void
  refresh: () => void
  markRead: (id: string) => void
}): Issue[] {
  const out: Issue[] = []
  const { fleet, servers, now, go } = o
  for (const r of o.requests ?? []) {
    if (r.status !== 'pending') continue
    const expires = toMs(r.expires_at)
    if (expires && expires < now) continue
    out.push({
      key: 'req' + r.id,
      tone: 'warn',
      icon: 'bot',
      lead: `${r.client_name} wants to ${actionPhrase(r.action)} ${r.server_name || 'a server'}.`,
      rest: expires ? `The request expires ${relTime(expires, now)}.` : undefined,
      acts: r.requires_password
        ? [
            {
              label: 'Review',
              primary: true,
              run: () =>
                useMcpApprovalDialog.getState().open({
                  requestId: r.id,
                  serverId: r.server_id,
                  serverName: r.server_name,
                  action: r.action,
                  clientName: r.client_name,
                  reason: r.reason,
                  requiresPassword: true,
                  expiresAt: r.expires_at,
                }),
            },
          ]
        : [
            {
              label: 'Approve',
              primary: true,
              run: () =>
                void act(`approve ${r.client_name}’s request`, () => api.mcp.actionRequests.approve(r.id)).then((res) => {
                  if (!res) return
                  if (res.status === 'failed') toast('bad', 'The action failed', res.failure_reason ?? 'The server didn’t accept it.')
                  else toast('ok', `Approved: ${actionPhrase(r.action)}`)
                  o.refresh()
                }),
            },
            { label: 'Deny', text: true, run: () => void act(`deny ${r.client_name}’s request`, () => api.mcp.actionRequests.deny(r.id)).then(o.refresh) },
          ],
    })
  }
  for (const s of servers) {
    if (failedStart(s)) {
      out.push(
        s.active_conflict
          ? {
              key: 'crash' + s.id,
              tone: 'bad',
              lead: `${s.name} didn’t start.`,
              rest: 'The panel found what stopped it and how to fix it.',
              serverId: s.id,
              acts: [{ label: 'Fix it', primary: true, run: () => go(paths.server(s.id)) }],
            }
          : {
              key: 'crash' + s.id,
              tone: 'bad',
              lead: `${s.name} ${s.status === 'startup_failure' ? 'didn’t start' : 'crashed'}.`,
              rest: 'The console shows what happened.',
              serverId: s.id,
              acts: [
                { label: 'Start it', primary: true, need: 'power.start', run: () => o.start(s) },
                { label: 'Console', text: true, run: () => go(paths.server(s.id)) },
              ],
            },
      )
    }
    if (s.status === 'online' && s.tps != null && s.tps < 17.5) {
      out.push({
        key: 'tps' + s.id,
        tone: 'warn',
        icon: 'activity',
        lead: `${s.name} is lagging at ${s.tps.toFixed(1)} TPS.`,
        serverId: s.id,
        acts: [{ label: 'See stats', run: () => go(paths.server(s.id, 'stats')) }],
      })
    }
    const last = toMs(s.last_backup_at)
    if (backupFailing(s)) {
      const reason = s.backup_error ? s.backup_error.charAt(0).toLowerCase() + s.backup_error.slice(1).replace(/\.$/, '') : ''
      out.push({
        key: 'bk' + s.id,
        tone: 'warn',
        icon: 'archive',
        lead: last ? `${s.name} hasn’t had a good backup in ${howLong(now - last)}.` : `${s.name} has never been backed up.`,
        rest: reason ? `The last backup failed: ${reason}.` : 'The last backup failed.',
        serverId: s.id,
        acts: [{ label: 'Backups', run: () => go(paths.server(s.id, 'backups')) }],
      })
    } else if (last && backupLate(s)) {
      out.push({
        key: 'bk' + s.id,
        tone: 'warn',
        icon: 'archive',
        lead: `${s.name} hasn’t had a backup in ${howLong(now - last)}.`,
        serverId: s.id,
        acts: [{ label: 'Backups', run: () => go(paths.server(s.id, 'backups')) }],
      })
    } else if (!last && played(s)) {
      out.push({
        key: 'bk' + s.id,
        tone: 'warn',
        icon: 'archive',
        lead: `${s.name} has never been backed up.`,
        serverId: s.id,
        acts: [
          {
            label: 'Back up now',
            need: 'backups.create',
            run: () => void act(`back up ${s.name}`, () => api.backups.create(s.id)).then((r) => r !== undefined && toast('info', `Backing up ${s.name}`)),
          },
        ],
      })
    }
  }
  for (const n of fleet.nodes) {
    if (n.online) continue
    const hosted = servers.filter((s) => s.node_id === n.id)
    const seen = toMs(n.last_seen)
    out.push({
      key: 'node' + n.id,
      tone: 'bad',
      icon: 'plug-off',
      lead: seen ? `${n.name} has been offline since ${now - seen > DAY ? relTime(seen, now) : fmtClock(seen)}.` : `${n.name} hasn’t been seen yet.`,
      rest: hosted.length ? `${joinNames(hosted.map((s) => s.name))} can’t run until it’s back.` : undefined,
      acts: [{ label: 'Node', run: () => go(paths.nodes) }],
    })
  }
  for (const item of o.feed) {
    if (item.read_at) continue
    const denied = asJoinDeniedAlert(item)
    if (!denied) continue
    const s = servers.find((x) => x.id === denied.serverId)
    if (!s) continue
    const times = denied.attempts > 1 ? ' ' + (['', 'once', 'twice', 'three times', 'four times'][denied.attempts] || `${denied.attempts} times`) : ''
    const markRead = () => {
      o.markRead(item.id)
      void api.notifications.markRead(item.id).catch(() => {})
    }
    out.push({
      key: 'deny' + item.id,
      tone: 'warn',
      icon: 'user-x',
      lead: `${denied.player} tried to join ${s.name}${times}.`,
      rest: 'They aren’t on the whitelist.',
      serverId: s.id,
      acts: [
        {
          label: 'Add to whitelist',
          need: 'players.whitelist',
          run: () =>
            void act(`whitelist ${denied.player}`, () => whitelistFromAlert(denied)).then((r) => {
              if (r === undefined) return
              markRead()
              toast('ok', `${denied.player} is on the whitelist`)
            }),
        },
        { label: 'Ignore', text: true, run: markRead },
      ],
    })
  }
  return out.sort((a, b) => (a.tone === b.tone ? 0 : a.tone === 'bad' ? -1 : 1))
}

function joinNames(names: string[]): string {
  if (names.length <= 1) return names.join('')
  return names.slice(0, -1).join(', ') + ' and ' + names[names.length - 1]
}

function IssueList({ items, permsFor }: { items: Issue[]; permsFor: (id: string) => MyServerPermissions | null }) {
  return (
    <div className="issues">
      {items.map((it) => {
        const body = (
          <div className="issue glow">
            <span className={cx('mark', it.tone)}>
              <Icon name={it.icon || (it.tone === 'bad' ? 'alert-circle' : 'alert')} size={17} />
            </span>
            <p>
              <b>{it.lead}</b> {it.rest}
            </p>
            <div className="acts">
              {it.acts.map((a, i) =>
                a.text ? (
                  <Btn key={i} size="sm" variant="quiet" onClick={a.run}>
                    {a.label}
                  </Btn>
                ) : (
                  <Btn key={i} size="sm" variant={a.primary ? 'primary' : ''} need={a.need} onClick={a.run}>
                    {a.label}
                  </Btn>
                ),
              )}
            </div>
          </div>
        )
        return it.serverId ? (
          <PermScope key={it.key} perms={permsFor(it.serverId)}>
            {body}
          </PermScope>
        ) : (
          <div key={it.key} style={{ display: 'contents' }}>
            {body}
          </div>
        )
      })}
    </div>
  )
}

// ---- figures ---------------------------------------------------------------------------------

/** One figure in Home's strip: a label, a number, and a line or picture under it. */
function Kpi({ label, value, tone, onClick, aside, children }: { label: string; value: ReactNode; tone?: string; onClick?: () => void; aside?: ReactNode; children?: ReactNode }) {
  return (
    <div
      className={cx('kpi', onClick && 'click')}
      onClick={onClick}
      role={onClick ? 'link' : undefined}
      tabIndex={onClick ? 0 : undefined}
      onKeyDown={onClick ? (e) => e.key === 'Enter' && onClick() : undefined}
    >
      <div className="kpi-l">{label}</div>
      <div className="kpi-v">
        <span className={cx('num', tone)}>{value}</span>
        {aside}
      </div>
      <div className="kpi-s">{children}</div>
    </div>
  )
}

/** One bar per hour. An empty hour is a thin baseline, not a stub. */
export function hourBars(values: (number | null)[], max: number, nowUp: boolean) {
  return values.map((raw, i) => {
    const v = Math.round(raw ?? 0)
    return <i key={i} className={cx(!v ? 'zero' : i === values.length - 1 && nowUp ? 'now' : 'on')} style={v ? { height: Math.max(8, (v / max) * 100) + '%' } : undefined} />
  })
}

// ---- servers ---------------------------------------------------------------------------------

function backupCell(s: FleetServer, now: number) {
  const failed = toMs(s.backup_failed_at)
  if (failed) {
    return (
      <span className="bad" data-tip={s.backup_error || 'The last backup failed'}>
        failed {relTime(failed, now)}
      </span>
    )
  }
  const at = toMs(s.last_backup_at)
  if (!at) return <span className="t3">never</span>
  return <span className={backupLate(s) ? 'warn' : 't2'}>{relTime(at, now)}</span>
}

function ServerRow({
  s,
  status,
  node,
  now,
  onOpen,
  onStart,
  onRestart,
}: {
  s: FleetServer
  status: StatusKey
  node: string
  now: number
  onOpen: () => void
  onStart: () => void
  onRestart: () => void
}) {
  const up = s.status === 'online'
  const live = isLive(s)
  const on = s.players ?? 0
  const day = s.players_24h.map((v) => v ?? 0)
  if (up && day.length) day[day.length - 1] = Math.max(day[day.length - 1], on)
  const max = Math.max(4, ...day)
  const [label, tone] = STATE[status] ?? [cap(status), '']
  const sub = status === 'online' ? uptime(s, now) : failedStart(s) && s.active_conflict ? 'on start' : s.operation ? s.operation.message || s.operation.phase : ''
  const memPct = live && s.ram_used_mb != null && s.ram_limit_mb ? (s.ram_used_mb / s.ram_limit_mb) * 100 : 0
  const updates = s.mod_updates ?? 0
  return (
    <div className={cx('srv-row', !live && !failedStart(s) && 'down')} role="link" tabIndex={0} onClick={onOpen} onKeyDown={(e) => e.key === 'Enter' && onOpen()}>
      <span className="cell name">
        <ServerIcon id={s.id} size={20} className="srv-row-icon" />
        <span className="strong ellipsis">{s.name}</span>
        <span className="t3 ellipsis small">
          {platformLabel(s.platform)} {s.mc_version}
        </span>
        {updates > 0 && (
          <span className="upd-dot" data-tip={plural(updates, 'update') + ' waiting'}>
            {updates}
          </span>
        )}
      </span>
      <span className={cx('cell status', tone === 'bad' && 'bad', tone === 'warn' && 'warn')}>
        <span className={cx('dot', tone)} />
        {label}
        {sub && <span className="t3 small ellipsis">{sub}</span>}
      </span>
      <span className="cell num r">{up && s.players != null ? <Num value={on} /> : <span className="t3">–</span>}</span>
      <span className={cx('cell num r', up && s.tps != null && s.tps < 18.5 && 'warn')}>{up && s.tps != null ? <Num value={s.tps} format={(v) => v.toFixed(1)} /> : <span className="t3">–</span>}</span>
      <span className="cell num r">{live && s.cpu_percent != null ? <Num value={s.cpu_percent} format={(v) => Math.round(v) + '%'} /> : <span className="t3">–</span>}</span>
      <span className="cell mem">
        {live && s.ram_used_mb != null ? (
          <>
            <Meter value={memPct} max={100} tone={memPct > 90 ? 'bad' : memPct > 80 ? 'warn' : ''} label="Memory" />
            <span className="num small t2">{fmtMB(s.ram_used_mb, 1)}</span>
          </>
        ) : (
          <span className="t3">–</span>
        )}
      </span>
      <span className="cell bars-cell">
        <span className="bars" data-tip="Players, last 24 hours">
          {hourBars(day, max, up)}
        </span>
      </span>
      <span className="cell t2 ellipsis">{node}</span>
      <span className="cell small">{backupCell(s, now)}</span>
      <span className="acts" onClick={(e) => e.stopPropagation()}>
        {failedStart(s) && s.active_conflict ? (
          <Btn size="sm" onClick={onOpen}>
            Fix it
          </Btn>
        ) : live ? (
          <Btn size="sm" need="power.restart" disabled={isBusy(s)} onClick={onRestart}>
            Restart
          </Btn>
        ) : status !== 'unreachable' ? (
          <Btn size="sm" need="power.start" disabled={isBusy(s)} onClick={onStart}>
            Start
          </Btn>
        ) : null}
      </span>
    </div>
  )
}

// ---- around the servers ----------------------------------------------------------------------

function NodesWidget({ nodes, onOpen }: { nodes: FleetNode[]; onOpen?: () => void }) {
  const pct = (a: number | null, b: number | null) => (a != null && b ? Math.round((a / b) * 100) : null)
  return (
    <section className="tile widget">
      <div className="tile-head">
        <h2 className="tile-title">Nodes</h2>
        {onOpen && (
          <Btn size="sm" variant="quiet" onClick={onOpen} style={{ marginLeft: 'auto' }}>
            All nodes
          </Btn>
        )}
      </div>
      {nodes.length ? (
        <div className="mini-t">
          <div className="mini-row head" aria-hidden="true">
            <span>Node</span>
            <span>CPU</span>
            <span>Memory</span>
            <span>Disk</span>
          </div>
          {nodes.map((n) => {
            const mem = pct(n.mem_used_mb, n.memory_mb)
            const disk = pct(n.disk_used_gb, n.disk_gb)
            const seen = toMs(n.last_seen)
            return (
              <div key={n.id} className={cx('mini-row', onOpen && 'click')} onClick={onOpen}>
                <span className="row" style={{ gap: 8, minWidth: 0 }}>
                  <Status status={n.online ? 'online' : 'crashed'} quiet />
                  <span className="strong ellipsis">{n.name}</span>
                </span>
                {n.online ? (
                  <>
                    <span className="mini-meter">
                      <Meter value={n.cpu_pct ?? 0} max={100} label="CPU" />
                      <span className="num">{n.cpu_pct != null ? Math.round(n.cpu_pct) + '%' : '–'}</span>
                    </span>
                    <span className="mini-meter">
                      <Meter value={mem ?? 0} max={100} label="Memory" />
                      <span className="num">{mem != null ? mem + '%' : '–'}</span>
                    </span>
                    <span className="mini-meter">
                      <Meter value={disk ?? 0} max={100} label="Disk" />
                      <span className="num">{disk != null ? disk + '%' : '–'}</span>
                    </span>
                  </>
                ) : (
                  <span className="bad small" style={{ gridColumn: 'span 3' }}>
                    {seen ? 'offline since ' + fmtClock(seen) : 'not seen yet'}
                  </span>
                )}
              </div>
            )
          })}
        </div>
      ) : (
        <p className="t3 small">No nodes yet.</p>
      )}
    </section>
  )
}


function ActivityMini({ entries }: { entries: AuditEntryWithActor[] }) {
  const shown = entries.filter((e) => e.action !== 'auth.login').slice(0, 6)
  if (!shown.length) return <p className="t3 small">Nothing yet.</p>
  return (
    <div className="mini-t">
      {shown.map((e) => {
        const t = auditSentence(e)
        return (
          <div key={e.id} className="mini-row pair">
            <span className="num t3">{fmtClock(toMs(e.created_at) ?? Date.now())}</span>
            <span className="ellipsis" data-tip={e.detail || undefined}>
              <span className={cx('strong', t.bad && 'bad')}>{t.who}</span> {t.verb}
              {t.obj ? ' ' + t.obj : ''}
            </span>
          </div>
        )
      })}
    </div>
  )
}
