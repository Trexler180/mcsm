import { Fragment, useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { desktopNotificationsEnabled, disableDesktopNotifications, enableDesktopNotifications, notificationPermission, notificationsSupported } from '@/lib/desktop-notify'
import { asJoinDeniedAlert, canWhitelistOn, whitelistFromAlert } from '@/lib/join-denied'
import { asMcpActionAlert, resolvedRequestId } from '@/lib/mcp-action-alert'
import type { NotificationChannel, NotificationEventDef, NotificationItem, NotificationSeverity, NotificationSubscription } from '@/lib/types'
import { disablePush, enablePush, pushSubscription, pushSupported } from '@/lib/web-push'
import { useMcpApprovalDialog } from '@/shell/agent-review'
import { useNotificationFeed } from '@/store/notification-feed'
import { useFleet } from '@/shell/fleet'
import { useGo } from '@/shell/nav'
import { paths } from '@/shell/paths'
import { Btn, Check, Empty, Field, Filters, IconBtn, Select, Switch, Tabs, TextBtn } from '@/ui/controls'
import { act, failureOf } from '@/ui/failure'
import { fmtClock, fmtDay } from '@/ui/format'
import { MenuButton, MenuItem } from '@/ui/overlays'
import { PageHead } from '@/ui/page'
import { Loaded, SkItems, SkTable } from '@/ui/skeleton'
import { ask } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { cx } from '@/ui/util'

// Alerts: what happened (the inbox), what to hear about and where (rules),
// and the places alerts can go (this browser, webhooks).

export type AlertsTab = 'inbox' | 'rules' | 'delivery'

export function AlertsPage({ tab, setTab }: { tab: AlertsTab; setTab: (t: AlertsTab) => void }) {
  const unread = useNotificationFeed((s) => s.unread)
  return (
    <div className="scroll">
      <div className="page narrow">
        <PageHead title="Alerts" />
        <Tabs
          inset
          value={tab}
          onChange={setTab}
          label="Alerts"
          items={[
            { value: 'inbox', label: 'Inbox', count: unread || undefined },
            { value: 'rules', label: 'Rules' },
            { value: 'delivery', label: 'Delivery' },
          ]}
        />
        <div style={{ marginTop: 22 }}>
          {tab === 'inbox' && <Inbox onRules={() => setTab('rules')} />}
          {tab === 'rules' && <Rules />}
          {tab === 'delivery' && <Delivery />}
        </div>
      </div>
    </div>
  )
}

// ---- inbox --------------------------------------------------------------------------------------

/** The button an alert offers, if there's something to do about it. */
export function alertLink(item: NotificationItem): { label: string; to: string } | null {
  const s = item.server_id
  const t = item.event_type
  if ((t === 'server.crash' || t === 'server.start_failed') && s) return { label: 'Console', to: paths.server(s) }
  if ((t === 'server.tps_low' || t === 'server.performance') && s) return { label: 'Stats', to: paths.server(s, 'stats') }
  if (t.startsWith('mod.') && s) return { label: 'Mods', to: paths.server(s, 'mods') }
  if (t.startsWith('backup.') && s) return { label: 'Backups', to: paths.server(s, 'backups') }
  if (t === 'schedule.failed' && s) return { label: 'Schedules', to: paths.server(s, 'schedules') }
  if (t === 'node.offline' || t === 'node.disk_low') return { label: 'Nodes', to: paths.nodes }
  if (t === 'auth.new_device') return { label: 'Sessions', to: paths.account }
  return null
}

function Inbox({ onRules }: { onRules: () => void }) {
  const items = useNotificationFeed((s) => s.items)
  const unread = useNotificationFeed((s) => s.unread)
  const [f, setF] = useState<'all' | 'unread'>('all')
  const fleet = useFleet().data
  const serverName = (id: string | null) => (id ? fleet?.servers.find((x) => x.id === id)?.name : undefined)
  // Requests answered since they were raised, so their alerts stop offering Review.
  const resolved = useMemo(() => new Set(items.map(resolvedRequestId).filter((x): x is string => !!x)), [items])
  const list = items.filter((n) => f === 'all' || !n.read_at)
  const read = (n: NotificationItem) => {
    if (n.read_at) return
    useNotificationFeed.getState().markRead(n.id)
    void api.notifications.markRead(n.id).catch(() => undefined)
  }
  const readAll = () =>
    void act('mark everything as read', () => api.notifications.markAllRead()).then((r) => {
      if (r !== undefined) useNotificationFeed.getState().markAllRead()
    })
  let lastDay = ''
  return (
    <>
      <div className="toolbar">
        <Filters
          value={f}
          onChange={setF}
          label="Show"
          options={[
            { value: 'all', label: 'All' },
            { value: 'unread', label: 'Unread', count: unread || undefined },
          ]}
        />
        <span className="grow" />
        {unread > 0 && (
          <TextBtn quiet onClick={readAll}>
            Mark all as read
          </TextBtn>
        )}
      </div>
      {list.length === 0 ? (
        <Empty title={f === 'unread' ? 'You’ve read everything.' : 'No alerts yet.'}>{f === 'all' && 'Choose what you hear about under Rules.'}</Empty>
      ) : (
        <div className="items">
          {list.map((n) => {
            const at = Date.parse(n.created_at)
            const day = fmtDay(at)
            const head = day !== lastDay ? day : null
            lastDay = day
            const name = serverName(n.server_id)
            return (
              <Fragment key={n.id}>
                {head && <div className="item day t3 small">{head}</div>}
                <div className="item" style={{ alignItems: 'flex-start' }} onClick={() => read(n)}>
                  <span className={cx('dot', n.severity === 'critical' ? 'bad' : n.severity === 'warning' ? 'warn' : '')} style={{ marginTop: 7, animation: 'none', opacity: n.read_at ? 0.45 : 1 }} />
                  <div className="grow">
                    <div style={{ fontWeight: n.read_at ? 400 : 600 }}>{n.title}</div>
                    {n.body && <div className="t2 small">{n.body}</div>}
                    <div className="t3 tiny" style={{ marginTop: 3 }}>
                      {fmtClock(at)}
                      {name ? `, ${name}` : ''}
                    </div>
                  </div>
                  <AlertAction item={n} resolved={resolved} onAct={() => read(n)} />
                  {!n.read_at && (
                    <MenuButton
                      label="More"
                      items={(close) => (
                        <>
                          <MenuItem close={close} onClick={() => read(n)}>
                            Mark as read
                          </MenuItem>
                          <MenuItem close={close} onClick={onRules}>
                            Change what I hear about
                          </MenuItem>
                        </>
                      )}
                    />
                  )}
                </div>
              </Fragment>
            )
          })}
        </div>
      )}
      {items.length >= 50 && <p className="t3 small" style={{ marginTop: 14 }}>Showing the newest {items.length}.</p>}
    </>
  )
}

function AlertAction({ item, resolved, onAct }: { item: NotificationItem; resolved: Set<string>; onAct: () => void }) {
  const go = useGo()
  const denied = asJoinDeniedAlert(item)
  const request = asMcpActionAlert(item)
  const [done, setDone] = useState<string | null>(null)
  const [canWhitelist, setCanWhitelist] = useState(false)
  useEffect(() => {
    if (denied) void canWhitelistOn(denied.serverId).then(setCanWhitelist)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [item.id])
  const stop = (e: React.MouseEvent) => e.stopPropagation()
  if (done) return <span className="t3 small" style={{ paddingTop: 2 }}>{done}</span>
  if (denied) {
    if (!canWhitelist) return null
    return (
      <Btn
        size="sm"
        onClick={(e) => {
          stop(e)
          onAct()
          void act(`whitelist ${denied.player}`, () => whitelistFromAlert(denied)).then((r) => {
            if (r === undefined) return
            setDone('Whitelisted')
            toast('ok', `${denied.player} can join now`)
          })
        }}
      >
        Add to whitelist
      </Btn>
    )
  }
  if (request) {
    if (resolved.has(request.requestId)) return <span className="t3 small" style={{ paddingTop: 2 }}>Answered</span>
    if (request.expiresAt && Date.parse(request.expiresAt) < Date.now()) return <span className="t3 small" style={{ paddingTop: 2 }}>Expired</span>
    return (
      <Btn
        size="sm"
        onClick={(e) => {
          stop(e)
          onAct()
          useMcpApprovalDialog.getState().open(request)
        }}
      >
        Review
      </Btn>
    )
  }
  const link = alertLink(item)
  if (!link) return null
  return (
    <Btn
      size="sm"
      onClick={(e) => {
        stop(e)
        onAct()
        go(link.to)
      }}
    >
      {link.label}
    </Btn>
  )
}

// ---- rules ----------------------------------------------------------------------------------------

const SEVERITIES: { value: NotificationSeverity; label: string }[] = [
  { value: 'info', label: 'Everything' },
  { value: 'warning', label: 'Warnings up' },
  { value: 'critical', label: 'Critical only' },
]

interface Channel {
  id: string
  label: string
  off?: boolean
}

export function channelsFor(webhooks: NotificationChannel[]): Channel[] {
  return [
    { id: 'inapp', label: 'Panel' },
    { id: 'webpush', label: 'Push' },
    ...webhooks.map((w) => ({ id: `webhook:${w.id}`, label: w.label || 'Webhook', off: !w.enabled })),
  ]
}

function Rules() {
  const qc = useQueryClient()
  const events = useQuery({ queryKey: ['notif-events'], queryFn: () => api.notifications.events(), staleTime: 60 * 60_000 })
  const subs = useQuery({ queryKey: ['notif-subs'], queryFn: () => api.notifications.subscriptions.list() })
  const webhooks = useQuery({ queryKey: ['notif-channels'], queryFn: () => api.notifications.channels.list() }).data ?? []
  const chans = channelsFor(webhooks)
  const byType = useMemo(() => new Map((subs.data ?? []).filter((s) => s.server_id === null).map((s) => [s.event_type, s])), [subs.data])
  const save = (def: NotificationEventDef, next: { enabled?: boolean; channels?: string[]; min_severity?: NotificationSeverity }) => {
    const cur = byType.get(def.type)
    const body = {
      event_type: def.type,
      server_id: null,
      enabled: next.enabled ?? cur?.enabled ?? false,
      channels: next.channels ?? cur?.channels ?? ['inapp'],
      min_severity: next.min_severity ?? cur?.min_severity ?? def.default_severity,
    }
    // Shown straight away; put back if the panel says no.
    qc.setQueryData<NotificationSubscription[]>(['notif-subs'], (list = []) => {
      const others = list.filter((s) => !(s.event_type === def.type && s.server_id === null))
      return [...others, { ...(cur ?? { id: '', user_id: '', created_at: '', updated_at: '' }), ...body } as NotificationSubscription]
    })
    void act(`change the ${def.label} rule`, () => api.notifications.subscriptions.upsert(body)).finally(() => void qc.invalidateQueries({ queryKey: ['notif-subs'] }))
  }
  return (
    <>
      <p className="t2" style={{ marginBottom: 18 }}>
        Choose what you hear about and where. Push reaches browsers you’ve turned it on in; webhooks are set up under Delivery.
      </p>
      <Loaded query={events} sk={<SkTable rows={8} />} what="the alert rules">
        {(defs) => (
          <div style={{ overflowX: 'auto' }}>
            <table className="t rules-t">
              <thead>
                <tr>
                  <th>Event</th>
                  <th style={{ width: 150 }}>Level</th>
                  {chans.map((c) => (
                    <th key={c.id} className="c" style={{ width: 76 }}>
                      {c.label}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {defs.map((def) => {
                  const sub = byType.get(def.type)
                  const on = !!sub?.enabled
                  const selected = sub?.channels ?? ['inapp']
                  return (
                    <tr key={def.type} className={on ? undefined : 'muted'}>
                      <td>
                        <div className="row" style={{ gap: 12, alignItems: 'flex-start' }}>
                          <Switch checked={on} label={def.label} onChange={(v) => save(def, { enabled: v })} />
                          <span>
                            <span style={{ fontWeight: 500 }}>{def.label}</span>
                            <div className="sub">{def.description}</div>
                          </span>
                        </div>
                      </td>
                      <td>
                        <Select size="sm" value={sub?.min_severity ?? def.default_severity} disabled={!on} onChange={(v) => save(def, { min_severity: v })} options={SEVERITIES} aria-label={`${def.label} level`} />
                      </td>
                      {chans.map((c) => (
                        <td key={c.id} className="c">
                          <Check
                            checked={selected.includes(c.id)}
                            disabled={!on || c.off}
                            label={`${def.label} in ${c.label}`}
                            hideLabel
                            onChange={(v) => save(def, { channels: v ? [...selected, c.id] : selected.filter((x) => x !== c.id) })}
                          />
                        </td>
                      ))}
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </Loaded>
    </>
  )
}

// ---- delivery -------------------------------------------------------------------------------------

function useBrowserAlerts() {
  const [push, setPush] = useState<boolean | null>(null)
  const [desktop, setDesktop] = useState(desktopNotificationsEnabled())
  useEffect(() => {
    void pushSubscription()
      .then((s) => setPush(!!s))
      .catch(() => setPush(false))
  }, [])
  return { push, setPush, desktop, setDesktop }
}

function Delivery() {
  const qc = useQueryClient()
  const b = useBrowserAlerts()
  const [busy, setBusy] = useState<'push' | 'desktop' | null>(null)
  const channels = useQuery({ queryKey: ['notif-channels'], queryFn: () => api.notifications.channels.list() })
  const blocked = notificationPermission() === 'denied'
  const togglePush = async (on: boolean) => {
    setBusy('push')
    try {
      if (on) await enablePush()
      else await disablePush()
      b.setPush(on)
      toast('ok', on ? 'Push is on for this browser' : 'Push is off for this browser')
    } catch (e) {
      toast('bad', on ? 'Couldn’t turn on push' : 'Couldn’t turn off push', failureOf(e).message)
    } finally {
      setBusy(null)
    }
  }
  const toggleDesktop = async (on: boolean) => {
    setBusy('desktop')
    try {
      if (on) await enableDesktopNotifications()
      else disableDesktopNotifications()
      b.setDesktop(desktopNotificationsEnabled())
    } catch (e) {
      toast('bad', 'Couldn’t turn on desktop notifications', failureOf(e).message)
    } finally {
      setBusy(null)
    }
  }
  return (
    <div className="stack">
      <div className="form-section">
        <h2>This browser</h2>
        {blocked && <p className="lede warn">Notifications are blocked for this site. Allow them in the browser’s site settings to use these.</p>}
        <div className="setting">
          <div className="text">
            <div className="name">Push notifications</div>
            <div className="desc">{pushSupported() ? 'Reach this device even when MCSM isn’t open. Pick which alerts use Push under Rules.' : 'This browser can’t receive push notifications.'}</div>
          </div>
          <Switch checked={!!b.push} label="Push notifications" disabled={!pushSupported() || b.push === null || busy === 'push' || blocked} onChange={(v) => void togglePush(v)} />
        </div>
        <div className="setting">
          <div className="text">
            <div className="name">Desktop notifications</div>
            <div className="desc">{notificationsSupported() ? 'Also show panel alerts as system notifications while a tab is open.' : 'This browser can’t show notifications.'}</div>
          </div>
          <Switch checked={b.desktop} label="Desktop notifications" disabled={!notificationsSupported() || busy === 'desktop' || blocked} onChange={(v) => void toggleDesktop(v)} />
        </div>
      </div>
      <div className="form-section">
        <h2>Webhooks</h2>
        <p className="lede">Post alerts to Discord, Slack, or anything that takes JSON.</p>
        <Loaded query={channels} sk={<SkItems rows={2} />} what="your webhooks">
          {(list) => (list.length ? <Webhooks list={list} /> : null)}
        </Loaded>
        <NewWebhook onAdded={() => void qc.invalidateQueries({ queryKey: ['notif-channels'] })} />
      </div>
    </div>
  )
}

const FORMATS = [
  { value: 'discord', label: 'Discord' },
  { value: 'slack', label: 'Slack' },
  { value: 'generic', label: 'Plain JSON' },
]
const formatLabel = (f: string) => FORMATS.find((x) => x.value === f)?.label ?? f

function Webhooks({ list }: { list: NotificationChannel[] }) {
  const qc = useQueryClient()
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['notif-channels'] })
    void qc.invalidateQueries({ queryKey: ['notif-subs'] })
  }
  const test = (c: NotificationChannel) =>
    void act(`send a test to ${c.label}`, () => api.notifications.channels.test(c.id)).then((r) => {
      if (r === undefined) return
      if (r.ok) toast('ok', `Sent a test to ${c.label}`)
      else toast('bad', `${c.label} didn’t take the test`, 'Check the URL, then try again.')
    })
  const setOn = (c: NotificationChannel, enabled: boolean) =>
    void act(enabled ? `turn on ${c.label}` : `turn off ${c.label}`, () => api.notifications.channels.update(c.id, { label: c.label, url: c.config.url, format: c.config.format, enabled })).then(refresh)
  const remove = async (c: NotificationChannel) => {
    if (!(await ask({ title: `Delete ${c.label}?`, body: 'Alerts stop going there, and it’s taken out of your rules.', confirm: 'Delete', danger: true }))) return
    const r = await act(`delete ${c.label}`, () => api.notifications.channels.remove(c.id).then(() => true))
    if (r) toast('ok', `Deleted ${c.label}`)
    refresh()
  }
  return (
    <div className="items" style={{ marginBottom: 18 }}>
      {list.map((c) => (
        <div key={c.id} className={cx('item', !c.enabled && 'muted')}>
          <div className="grow" style={{ minWidth: 0 }}>
            <div className="title">
              {c.label} <span className="t3" style={{ fontWeight: 400 }}>{formatLabel(c.config.format)}</span>
            </div>
            <div className="meta mono ellipsis">{c.config.url}</div>
          </div>
          <TextBtn quiet disabled={!c.enabled} onClick={() => test(c)}>
            Send a test
          </TextBtn>
          <Switch checked={c.enabled} label={`${c.label} on`} onChange={(v) => setOn(c, v)} />
          <IconBtn icon="trash" label={`Delete ${c.label}`} onClick={() => void remove(c)} />
        </div>
      ))}
    </div>
  )
}

export function webhookUrlError(url: string): string | null {
  const u = url.trim()
  if (!u) return null
  try {
    const parsed = new URL(u)
    if (parsed.protocol !== 'https:') return 'Use an https:// address.'
    return null
  } catch {
    return 'That isn’t a web address.'
  }
}

function NewWebhook({ onAdded }: { onAdded: () => void }) {
  const [label, setLabel] = useState('')
  const [url, setUrl] = useState('')
  const [format, setFormat] = useState('discord')
  const [secret, setSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const urlError = webhookUrlError(url)
  // Guess the format from the address, until it's been picked by hand.
  const [picked, setPicked] = useState(false)
  useEffect(() => {
    if (picked) return
    if (/discord(app)?\.com\/api\/webhooks/.test(url)) setFormat('discord')
    else if (/hooks\.slack\.com/.test(url)) setFormat('slack')
  }, [url, picked])
  const add = async () => {
    setBusy(true)
    const name = label.trim() || formatLabel(format)
    const r = await act(`add ${name}`, () => api.notifications.channels.create({ label: name, url: url.trim(), format, secret: format === 'generic' && secret.trim() ? secret.trim() : undefined }))
    setBusy(false)
    if (r === undefined) return
    toast('ok', `Added ${name}`, 'Choose which alerts go there under Rules.')
    setLabel('')
    setUrl('')
    setSecret('')
    setPicked(false)
    onAdded()
  }
  return (
    <>
      <div className="form-grid">
        <Field label="Name">
          <input className="input" value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Staff Discord" />
        </Field>
        <Field label="Format">
          <Select
            value={format}
            onChange={(v) => {
              setFormat(v)
              setPicked(true)
            }}
            options={FORMATS}
            aria-label="Format"
          />
        </Field>
        <Field label="URL" className="span" error={urlError}>
          <input className="input mono" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://discord.com/api/webhooks/…" aria-label="URL" />
        </Field>
        {format === 'generic' && (
          <Field label="Signing secret" className="span" hint="Optional. Each delivery is signed with it, so the receiver can tell it came from MCSM.">
            <input className="input mono" type="password" value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="off" aria-label="Signing secret" />
          </Field>
        )}
      </div>
      <div style={{ marginTop: 14 }}>
        <Btn disabled={!url.trim() || !!urlError} loading={busy} onClick={() => void add()}>
          Add webhook
        </Btn>
      </div>
    </>
  )
}
