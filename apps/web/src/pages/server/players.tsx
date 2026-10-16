import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { usePlayerName } from '@/components/players/name-field'
import { enchantLabel, itemLabel, maxDurability } from '@/components/players/items'
import type { GeyserInfo, ItemStack, MyServerPermissions, Player, PlayerActionKind, PlayerDetail, Server } from '@/lib/types'
import { Btn, Check, Choices, Copy, Empty, Field, Filters, Search, TextBtn, Tabs, type StatusKey } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtClock, fmtDay, fmtDur, fmtNum, relTime } from '@/ui/format'
import { Dialog, MenuButton, MenuItem, MenuSep, Sheet } from '@/ui/overlays'
import { useCan } from '@/ui/perm'
import { PlayerHead } from '@/ui/pixel'
import { SheetSkeleton, SkTable, Loaded } from '@/ui/skeleton'
import { ask } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { copyText, cx, toMs } from '@/ui/util'
import { allowed } from './data'

// Players: everyone who has joined or been added, what they may do, and what
// they're carrying. Changes apply live while the server runs, and to its
// files while it's stopped.

type Show = 'all' | 'online' | 'ops' | 'banned' | 'bedrock'

const DONE: Record<PlayerActionKind, (n: string) => string> = {
  whitelist_add: (n) => `${n} is on the whitelist`,
  whitelist_remove: (n) => `Took ${n} off the whitelist`,
  op: (n) => `${n} is an operator now`,
  deop: (n) => `${n} isn’t an operator any more`,
  kick: (n) => `Kicked ${n}`,
  ban: (n) => `Banned ${n}`,
  pardon: (n) => `Unbanned ${n}`,
  ban_ip: (n) => `Banned ${n}`,
  pardon_ip: (n) => `Unbanned ${n}`,
}
const VERB: Record<PlayerActionKind, string> = {
  whitelist_add: 'whitelist',
  whitelist_remove: 'take off the whitelist',
  op: 'make an operator',
  deop: 'remove as operator',
  kick: 'kick',
  ban: 'ban',
  pardon: 'unban',
  ban_ip: 'ban',
  pardon_ip: 'unban',
}

export type PlayerAction = (kind: PlayerActionKind, name: string, opts?: { uuid?: string; reason?: string; level?: number; bedrock?: boolean; gamertag?: string; ip?: string }) => Promise<boolean>

function usePlayerAction(serverId: string): PlayerAction {
  const qc = useQueryClient()
  return async (kind, name, opts = {}) => {
    const target = opts.ip ?? name
    const r = await act(`${VERB[kind]} ${target}`, () => api.players.action(serverId, { action: kind, name: kind.endsWith('_ip') ? undefined : name, ...opts }))
    if (r === undefined) return false
    toast('ok', DONE[kind](target))
    // A running server writes its lists a moment after the command, so look
    // again once it has.
    const refresh = () => {
      void qc.invalidateQueries({ queryKey: ['players', serverId] })
      void qc.invalidateQueries({ queryKey: ['player-bans', serverId] })
      void qc.invalidateQueries({ queryKey: ['player-detail', serverId] })
      void qc.invalidateQueries({ queryKey: ['players-meta', serverId] })
    }
    refresh()
    setTimeout(refresh, 1500)
    return true
  }
}

const sinceMs = (iso?: string) => Date.now() - (toMs(iso) ?? Date.now())
/** Someone with saved data to look at: they have joined at least once. */
const inspectable = (p: Player) => !!p.uuid && (p.online || toMs(p.last_seen) != null)

export function PlayersTab({
  server,
  status,
  perms,
  player,
  setPlayer,
}: {
  server: Server
  status: StatusKey
  perms: MyServerPermissions | undefined
  player?: string
  setPlayer: (name: string | undefined) => void
}) {
  const up = status === 'online'
  const roster = useQuery({ queryKey: ['players', server.id], queryFn: () => api.players.list(server.id), refetchInterval: up ? 30_000 : false })
  const meta = useQuery({ queryKey: ['players-meta', server.id], queryFn: () => api.players.meta(server.id), staleTime: 60_000, retry: false }).data
  const bans = useQuery({ queryKey: ['player-bans', server.id], queryFn: () => api.players.bans(server.id), staleTime: 30_000, retry: false }).data
  const sessions = useQuery({ queryKey: ['player-sessions', server.id, 720], queryFn: () => api.players.sessions(server.id, { hours: 720 }), staleTime: 5 * 60_000, retry: false }).data
  return (
    <Loaded query={roster} sk={<SkTable rows={8} />} what={`players on ${server.name}`}>
      {(list) => <PlayersBody server={server} status={status} perms={perms} roster={list} meta={meta} ipBans={bans?.ips ?? []} played={playedByName(sessions?.sessions)} open={player} setOpen={setPlayer} />}
    </Loaded>
  )
}

/** Seconds played per player over the sessions given. */
function playedByName(sessions: { player_name: string; started_at: string; ended_at: string | null }[] | undefined): Map<string, number> {
  const out = new Map<string, number>()
  for (const s of sessions ?? []) {
    const a = toMs(s.started_at)
    const b = s.ended_at ? toMs(s.ended_at) : Date.now()
    if (a == null || b == null) continue
    out.set(s.player_name.toLowerCase(), (out.get(s.player_name.toLowerCase()) ?? 0) + Math.max(0, b - a))
  }
  return out
}

function PlayersBody({
  server,
  status,
  perms,
  roster,
  meta,
  ipBans,
  played,
  open,
  setOpen,
}: {
  server: Server
  status: StatusKey
  perms: MyServerPermissions | undefined
  roster: Player[]
  meta: GeyserInfo | undefined
  ipBans: { ip: string; reason?: string; source?: string; created?: string }[]
  played: Map<string, number>
  open?: string
  setOpen: (name: string | undefined) => void
}) {
  const [show, setShow] = useState<Show>('all')
  const [q, setQ] = useState('')
  const [adding, setAdding] = useState(false)
  const [banning, setBanning] = useState<Player | null>(null)
  const action = usePlayerAction(server.id)
  const up = status === 'online'
  const wl = meta?.whitelist_enabled
  const flt: Record<Show, (p: Player) => boolean> = {
    all: () => true,
    online: (p) => p.online,
    ops: (p) => !!p.op,
    banned: (p) => !!p.banned,
    bedrock: (p) => !!p.bedrock,
  }
  const counts = Object.fromEntries((Object.keys(flt) as Show[]).map((k) => [k, roster.filter(flt[k]).length])) as Record<Show, number>
  const list = roster
    .filter(flt[show])
    .filter((p) => !q || p.name.toLowerCase().includes(q.toLowerCase()))
    .sort((a, b) => Number(b.online) - Number(a.online) || (toMs(b.last_seen) ?? 0) - (toMs(a.last_seen) ?? 0) || a.name.localeCompare(b.name))
  const openPlayer = open ? roster.find((p) => p.name.toLowerCase() === open.toLowerCase()) : undefined
  return (
    <>
      <div className="toolbar">
        <Filters
          value={show}
          onChange={setShow}
          label="Show"
          options={[
            { value: 'all', label: 'Everyone', count: counts.all },
            { value: 'online', label: 'Online', count: counts.online },
            { value: 'ops', label: 'Operators', count: counts.ops },
            { value: 'banned', label: 'Banned', count: counts.banned + ipBans.length },
            ...(counts.bedrock ? [{ value: 'bedrock' as Show, label: 'Bedrock', count: counts.bedrock }] : []),
          ]}
        />
        <span className="grow" />
        <Search value={q} onChange={setQ} placeholder="Filter players" size="sm" style={{ width: 200 }} />
        <Btn need="players.whitelist" onClick={() => setAdding(true)}>
          Add a player
        </Btn>
      </div>
      {wl !== undefined && (
        <p className="small t2" style={{ margin: '4px 0 16px' }}>
          {wl ? (
            'The whitelist is on, so only people on it can join. '
          ) : (
            <>
              <span className="warn">The whitelist is off.</span> Anyone with the address can join.{' '}
            </>
          )}
          <WhitelistToggle server={server} on={wl} up={up} perms={perms} />
        </p>
      )}
      {list.length === 0 ? (
        <Empty title={q ? 'Nobody matches.' : show === 'all' ? 'Nobody here yet.' : 'Nobody here.'}>{!q && show === 'all' && 'Players show up after they join for the first time, or when you add them.'}</Empty>
      ) : (
        <table className="t">
          <thead>
            <tr>
              <th>Name</th>
              <th>Seen</th>
              <th className="hide-sm">Role</th>
              <th className="r hide-sm">Played, 30 days</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {list.map((p) => (
              <tr
                key={p.uuid || p.name}
                className={cx(inspectable(p) && allowed(perms, 'players.inspect') && 'click')}
                onClick={() => inspectable(p) && allowed(perms, 'players.inspect') && setOpen(p.name)}
              >
                <td>
                  <span className="row" style={{ gap: 10 }}>
                    <PlayerHead name={p.bedrock ? '.' + p.name : p.name} />
                    <span className="strong">{p.name}</span>
                    {p.bedrock && <span className="t3 small">Bedrock</span>}
                  </span>
                </td>
                <td className="nowrap">
                  {p.online ? (
                    <span className="status">
                      <span className="dot ok" />
                      Online for {fmtDur(sinceMs(p.joined_at), 1)}
                    </span>
                  ) : (
                    <span className="t2">{toMs(p.last_seen) ? relTime(toMs(p.last_seen)) : 'Never joined'}</span>
                  )}
                </td>
                <td className="hide-sm small">
                  <RoleText p={p} whitelistOn={!!wl} />
                </td>
                <td className="r num t2 hide-sm">{played.get(p.name.toLowerCase()) ? fmtDur(played.get(p.name.toLowerCase()), 1) : '–'}</td>
                <td className="actions">
                  <span className="row" style={{ gap: 2, justifyContent: 'flex-end' }}>
                    {p.online && (
                      <span className="hover-show">
                        <Btn
                          size="sm"
                          need="players.kick"
                          onClick={(e) => {
                            e.stopPropagation()
                            void action('kick', p.name, { uuid: p.uuid })
                          }}
                        >
                          Kick
                        </Btn>
                      </span>
                    )}
                    <PlayerMenu server={server} p={p} up={up} action={action} onBan={() => setBanning(p)} />
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {show === 'banned' && <IpBans bans={ipBans} action={action} />}
      <AddPlayerDialog server={server} open={adding} onClose={() => setAdding(false)} meta={meta} up={up} action={action} />
      <BanDialog server={server} player={banning} up={up} onClose={() => setBanning(null)} action={action} />
      {openPlayer && inspectable(openPlayer) && <PlayerSheet server={server} player={openPlayer} up={up} action={action} onBan={() => setBanning(openPlayer)} onClose={() => setOpen(undefined)} />}
    </>
  )
}

function RoleText({ p, whitelistOn }: { p: Player; whitelistOn: boolean }) {
  if (p.banned) return <span className="bad">Banned</span>
  const bits: React.ReactNode[] = []
  if (p.op) bits.push(p.op_level && p.op_level !== 4 ? `Operator, level ${p.op_level}` : 'Operator')
  if (whitelistOn && !p.whitelisted)
    bits.push(
      <span key="w" className="t3">
        Not whitelisted
      </span>,
    )
  return <>{bits.length ? bits.reduce<React.ReactNode[]>((acc, b, i) => (i ? [...acc, ', ', b] : [b]), []) : null}</>
}

/** Turns the whitelist on or off: by command while the server runs, in
 *  server.properties while it's stopped. */
function WhitelistToggle({ server, on, up, perms }: { server: Server; on: boolean; up: boolean; perms: MyServerPermissions | undefined }) {
  const qc = useQueryClient()
  const can = up ? allowed(perms, 'console') : allowed(perms, 'files.write')
  if (!can) return null
  const toggle = async () => {
    const next = !on
    const label = next ? 'turn the whitelist on' : 'turn the whitelist off'
    const r = await act(label, async () => {
      if (up) return api.servers.command(server.id, `whitelist ${next ? 'on' : 'off'}`)
      const text = await api.files.readContent(server.id, '/server.properties')
      const line = `white-list=${next}`
      const updated = /^white-list=.*$/m.test(text) ? text.replace(/^white-list=.*$/m, line) : text.replace(/\n?$/, '\n') + line + '\n'
      return api.files.writeContent(server.id, '/server.properties', updated)
    })
    if (r === undefined) return
    toast('ok', next ? 'The whitelist is on' : 'The whitelist is off')
    void qc.invalidateQueries({ queryKey: ['players-meta', server.id] })
  }
  return (
    <TextBtn quiet onClick={() => void toggle()}>
      {on ? 'Turn it off' : 'Turn it on'}
    </TextBtn>
  )
}

function PlayerMenu({ server, p, up, action, onBan }: { server: Server; p: Player; up: boolean; action: PlayerAction; onBan: () => void }) {
  const qc = useQueryClient()
  const remove = async () => {
    if (!p.uuid) return
    const ok = await ask({
      title: `Delete ${p.name}’s saved data?`,
      body: 'Their inventory, ender chest, stats and advancements are wiped. They start fresh next time they join.',
      confirm: 'Delete',
      danger: true,
      typeToConfirm: p.name,
    })
    if (!ok) return
    const r = await act(`delete ${p.name}’s data`, () => api.players.remove(server.id, p.uuid as string))
    if (r === undefined) return
    toast('ok', `Deleted ${p.name}’s data`)
    void qc.invalidateQueries({ queryKey: ['players', server.id] })
  }
  return (
    <MenuButton
      label={`More for ${p.name}`}
      items={(close) => (
        <>
          {p.whitelisted ? (
            <MenuItem close={close} need="players.whitelist" onClick={() => void action('whitelist_remove', p.name, { uuid: p.uuid, bedrock: p.bedrock || undefined })}>
              Take off the whitelist
            </MenuItem>
          ) : (
            <MenuItem close={close} need="players.whitelist" disabled={p.banned} onClick={() => void action('whitelist_add', p.name, { uuid: p.uuid, bedrock: p.bedrock || undefined })}>
              Add to the whitelist
            </MenuItem>
          )}
          {p.op ? (
            <MenuItem close={close} need="players.op" onClick={() => void action('deop', p.name, { uuid: p.uuid })}>
              Remove as operator
            </MenuItem>
          ) : (
            <MenuItem close={close} need="players.op" onClick={() => void action('op', p.name, { uuid: p.uuid })}>
              Make an operator
            </MenuItem>
          )}
          {p.uuid && (
            <MenuItem
              close={close}
              onClick={() => {
                void copyText(p.uuid as string).then((ok) => toast(ok ? 'ok' : 'bad', ok ? 'Copied the UUID' : 'Couldn’t copy the UUID'))
              }}
            >
              Copy UUID
            </MenuItem>
          )}
          <MenuSep />
          {p.banned ? (
            <MenuItem close={close} need="players.ban" onClick={() => void action('pardon', p.name, { uuid: p.uuid })}>
              Unban
            </MenuItem>
          ) : (
            <MenuItem danger close={close} need="players.ban" onClick={onBan}>
              Ban
            </MenuItem>
          )}
          {p.uuid && (
            <MenuItem danger close={close} need="players.delete" disabled={p.online || up} hint={p.online || up ? 'while stopped' : undefined} onClick={() => void remove()}>
              Delete saved data
            </MenuItem>
          )}
        </>
      )}
    />
  )
}

function IpBans({ bans, action }: { bans: { ip: string; reason?: string; source?: string; created?: string }[]; action: PlayerAction }) {
  const [ip, setIp] = useState('')
  const [reason, setReason] = useState('')
  const valid = /^\d{1,3}(\.\d{1,3}){3}$/.test(ip.trim()) || /^[0-9a-f:]+:[0-9a-f:]*$/i.test(ip.trim())
  const ban = async () => {
    if (!valid) return
    if (await action('ban_ip', ip.trim(), { ip: ip.trim(), reason: reason.trim() || undefined })) {
      setIp('')
      setReason('')
    }
  }
  return (
    <section className="section">
      <div className="section-head">
        <h2 className="section-title">Banned addresses</h2>
      </div>
      {bans.length > 0 && (
        <table className="t" style={{ marginBottom: 18 }}>
          <tbody>
            {bans.map((b) => (
              <tr key={b.ip}>
                <td className="mono" style={{ width: 170 }}>
                  {b.ip}
                </td>
                <td className="t2">
                  {b.reason || 'No reason given'}
                  {(b.source || b.created) && (
                    <div className="sub">
                      {[b.source, b.created ? relTime(toMs(b.created)) : null].filter(Boolean).join(', ')}
                    </div>
                  )}
                </td>
                <td className="actions">
                  <Btn size="sm" need="players.ban" onClick={() => void action('pardon_ip', b.ip, { ip: b.ip })}>
                    Unban
                  </Btn>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="row wrap" style={{ gap: 8, alignItems: 'flex-end' }}>
        <Field label="Ban an address">
          <input className="input mono" style={{ width: 170 }} value={ip} onChange={(e) => setIp(e.target.value)} placeholder="203.0.113.7" onKeyDown={(e) => e.key === 'Enter' && void ban()} />
        </Field>
        <Field label="Reason" className="grow">
          <input className="input" value={reason} onChange={(e) => setReason(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && void ban()} />
        </Field>
        <Btn need="players.ban" disabled={!valid} onClick={() => void ban()}>
          Ban
        </Btn>
      </div>
    </section>
  )
}

function BanDialog({ server, player, up, onClose, action }: { server: Server; player: Player | null; up: boolean; onClose: () => void; action: PlayerAction }) {
  const [reason, setReason] = useState('')
  useEffect(() => setReason(''), [player])
  if (!player) return null
  const ban = async () => {
    if (await action('ban', player.name, { uuid: player.uuid, reason: reason.trim() || undefined })) onClose()
  }
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Ban ${player.name} from ${server.name}?`}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="danger primary" onClick={() => void ban()}>
            Ban
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 14 }}>
        {player.online && up && <p className="t2">They’re online and will be disconnected.</p>}
        <Field label="Reason" hint="They see this when they try to join.">
          <input className="input" value={reason} onChange={(e) => setReason(e.target.value)} autoFocus onKeyDown={(e) => e.key === 'Enter' && void ban()} />
        </Field>
      </div>
    </Dialog>
  )
}

const OP_LEVELS = [
  { level: 1, desc: 'Can build in the spawn area.' },
  { level: 2, desc: 'Cheats too: /gamemode, /give, /tp, command blocks.' },
  { level: 3, desc: 'Manages players too: /kick, /ban, /op.' },
  { level: 4, desc: 'Full control: /stop, /save-all, the server itself.' },
]

function AddPlayerDialog({ server, open, onClose, meta, up, action }: { server: Server; open: boolean; onClose: () => void; meta: GeyserInfo | undefined; up: boolean; action: PlayerAction }) {
  const can = useCan()
  const name = usePlayerName(server.id, meta)
  const [op, setOp] = useState(false)
  const [level, setLevel] = useState(4)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (open) {
      name.reset()
      setOp(false)
      setLevel(4)
    }
    // Reset once per opening; the hook's reset is stable enough for that.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])
  const sel = name.selection
  const add = async () => {
    if (!sel.valid || busy) return
    setBusy(true)
    const bedrock = sel.edition === 'bedrock'
    const ok = await action('whitelist_add', sel.name, { bedrock: bedrock || undefined, gamertag: bedrock ? sel.gamertag : undefined })
    if (ok && op) await action('op', sel.name, up ? {} : { level })
    setBusy(false)
    if (ok) onClose()
  }
  const javaError = sel.edition === 'java' && name.trimmed && !sel.valid ? 'Usernames are 1 to 16 letters, numbers or underscores.' : null
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={`Add a player to ${server.name}`}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" disabled={!sel.valid} loading={busy} onClick={() => void add()}>
            Add {sel.name || 'player'}
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 16 }}>
        {name.bedrockAvailable && (
          <Choices
            value={name.edition}
            onChange={name.chooseEdition}
            columns={2}
            options={[
              { value: 'java', label: 'Java Edition' },
              { value: 'bedrock', label: 'Bedrock Edition', desc: 'Through Floodgate' },
            ]}
          />
        )}
        <Field
          label={name.edition === 'java' ? 'Username' : 'Xbox gamertag'}
          error={javaError ?? (name.resolveError ? `Couldn’t find that gamertag: ${name.resolveError}` : null)}
          hint={
            name.edition === 'bedrock'
              ? name.resolving
                ? 'Looking them up…'
                : name.resolved
                  ? `They’ll join as ${name.resolved.name}.`
                  : 'Floodgate gives Bedrock players their own name on the server.'
              : name.geyserOnly
                ? 'Geyser is installed without Floodgate, so Bedrock players sign in with a Java account. Add that.'
                : undefined
          }
        >
          <input className="input" value={name.input} onChange={(e) => name.setInput(e.target.value)} autoFocus onKeyDown={(e) => e.key === 'Enter' && void add()} />
        </Field>
        <Check checked={op} onChange={setOp} disabled={!can('players.op')} label="Make them an operator too" />
        {op && !up && (
          <div>
            <div className="field-label" style={{ marginBottom: 6 }}>
              Operator level
            </div>
            <Choices
              value={String(level)}
              onChange={(v) => setLevel(Number(v))}
              columns={2}
              options={OP_LEVELS.map((l) => ({ value: String(l.level), label: `Level ${l.level}`, desc: l.desc }))}
            />
          </div>
        )}
        <p className="small t3">
          {up ? 'This applies straight away.' : 'It’s saved to the server files and applies next time it starts.'}
          {op && up && ' A running server makes operators at its default level; stop it first to choose one.'}
          {meta?.whitelist_enabled === false && ' The whitelist is off at the moment, so this won’t change who can join.'}
        </p>
      </div>
    </Dialog>
  )
}

// ---- one player --------------------------------------------------------------------------

function ItemRows({ items, selected }: { items: ItemStack[]; selected?: number }) {
  if (!items.length)
    return (
      <p className="t3 small" style={{ padding: '8px 0' }}>
        Empty.
      </p>
    )
  return (
    <table className="t">
      <tbody>
        {items.map((it) => {
          const max = maxDurability(it.id)
          return (
            <tr key={it.slot}>
              <td>
                <span style={{ fontWeight: 500 }}>{it.custom_name || itemLabel(it.id)}</span>
                {selected === it.slot && <span className="t3 small"> in hand</span>}
                {it.enchantments && it.enchantments.length > 0 && <div className="sub">{it.enchantments.map((e) => enchantLabel(e.id, e.level)).join(', ')}</div>}
              </td>
              <td className="r num t2" style={{ width: 70 }}>
                {it.count > 1 ? '×' + it.count : ''}
              </td>
              <td className="r num t3 small" style={{ width: 120 }}>
                {max && it.damage ? `${fmtNum(max - it.damage)} of ${fmtNum(max)}` : ''}
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

const DIMENSIONS: Record<string, string> = { 'minecraft:overworld': 'The Overworld', 'minecraft:the_nether': 'The Nether', 'minecraft:the_end': 'The End' }
const MODES = ['Survival', 'Creative', 'Adventure', 'Spectator']

function PlayerSheet({ server, player, up, action, onBan, onClose }: { server: Server; player: Player; up: boolean; action: PlayerAction; onBan: () => void; onClose: () => void }) {
  const [tab, setTab] = useState<'inventory' | 'ender' | 'sessions' | 'stats'>('inventory')
  const detail = useQuery({ queryKey: ['player-detail', server.id, player.uuid], queryFn: () => api.players.get(server.id, player.uuid as string), retry: false })
  const p = player
  return (
    <Sheet
      open
      onClose={onClose}
      wide
      icon={<PlayerHead name={p.bedrock ? '.' + p.name : p.name} size={44} />}
      title={p.name}
      sub={
        <span className="row" style={{ gap: 10 }}>
          {p.uuid && <Copy text={p.uuid} mono />}
          {p.bedrock && <span>Bedrock, through Geyser</span>}
        </span>
      }
      foot={
        <>
          {p.online && up && (
            <Btn need="players.kick" onClick={() => void action('kick', p.name, { uuid: p.uuid })}>
              Kick
            </Btn>
          )}
          {p.op ? (
            <Btn need="players.op" onClick={() => void action('deop', p.name, { uuid: p.uuid })}>
              Remove as operator
            </Btn>
          ) : (
            <Btn need="players.op" onClick={() => void action('op', p.name, { uuid: p.uuid })}>
              Make an operator
            </Btn>
          )}
          <span className="grow" />
          {p.banned ? (
            <Btn need="players.ban" onClick={() => void action('pardon', p.name, { uuid: p.uuid })}>
              Unban
            </Btn>
          ) : (
            <Btn variant="danger" need="players.ban" onClick={onBan}>
              Ban
            </Btn>
          )}
        </>
      }
    >
      <Loaded query={detail} sk={<SheetSkeleton />} what={`${p.name}’s saved data`}>
        {(d) => (
          <>
            <dl className="facts four" style={{ marginBottom: 22 }}>
              <dt>Status</dt>
              <dd>
                {p.banned ? <span className="bad">Banned{p.ban_reason ? `: ${p.ban_reason}` : ''}</span> : p.online ? `Online for ${fmtDur(sinceMs(p.joined_at), 1)}` : toMs(p.last_seen) ? `Last seen ${relTime(toMs(p.last_seen))}` : 'Never joined'}
              </dd>
              <dt>Played</dt>
              <dd>{d.stats?.play_time_ticks ? fmtDur(d.stats.play_time_ticks * 50, 2) : '–'}</dd>
              <dt>Game mode</dt>
              <dd>{MODES[d.game_mode] ?? '–'}</dd>
              <dt>Health</dt>
              <dd className="num">
                {Math.round(d.health)} of {Math.round(d.max_health || 20)}, hunger {d.food} of 20, level {d.xp_level}
              </dd>
              <dt>Where</dt>
              <dd>
                {DIMENSIONS[d.dimension] ?? d.dimension},{' '}
                <Copy text={d.pos.map((v) => Math.floor(v)).join(' ')} mono>
                  {d.pos.map((v) => Math.floor(v)).join(' ')}
                </Copy>
              </dd>
              <dt>Access</dt>
              <dd>{[d.op && 'Operator', d.whitelisted ? 'Whitelisted' : 'Not whitelisted'].filter(Boolean).join(', ')}</dd>
            </dl>
            <Tabs
              inset
              value={tab}
              onChange={setTab}
              items={[
                { value: 'inventory', label: 'Inventory' },
                { value: 'ender', label: 'Ender chest' },
                { value: 'sessions', label: 'Sessions' },
                { value: 'stats', label: 'Stats' },
              ]}
            />
            <div style={{ marginTop: 14 }}>
              {tab === 'inventory' && <Inventory d={d} online={p.online} />}
              {tab === 'ender' && <ItemRows items={d.ender_chest} />}
              {tab === 'sessions' && <Sessions serverId={server.id} name={p.name} />}
              {tab === 'stats' && <Stats d={d} />}
            </div>
          </>
        )}
      </Loaded>
    </Sheet>
  )
}

function Inventory({ d, online }: { d: PlayerDetail; online: boolean }) {
  // Slots 100-103 are armour, -106 the off hand, 0-8 the hotbar.
  const worn = d.inventory.filter((i) => i.slot >= 100 || i.slot === -106)
  const hotbar = d.inventory.filter((i) => i.slot >= 0 && i.slot < 9)
  const main = d.inventory.filter((i) => i.slot >= 9 && i.slot < 100)
  const saved = toMs(d.snapshot_at)
  return (
    <>
      <div className="field-label t2" style={{ margin: '4px 0 2px' }}>
        Wearing
      </div>
      <ItemRows items={worn} />
      <div className="field-label t2" style={{ margin: '18px 0 2px' }}>
        Hotbar
      </div>
      <ItemRows items={hotbar} selected={d.selected_slot} />
      <div className="field-label t2" style={{ margin: '18px 0 2px' }}>
        Inventory
      </div>
      <ItemRows items={main} />
      <p className="t3 small" style={{ marginTop: 12 }}>
        {online ? 'From the last world save, which happens every few minutes.' : 'As of when they logged out.'}
        {saved ? ` Saved ${relTime(saved)}.` : ''}
      </p>
    </>
  )
}

function Sessions({ serverId, name }: { serverId: string; name: string }) {
  const q = useQuery({ queryKey: ['player-sessions', serverId, name], queryFn: () => api.players.sessions(serverId, { name, hours: 720 }), staleTime: 60_000 })
  return (
    <Loaded query={q} sk={<SheetSkeleton />} what="their sessions">
      {(r) =>
        r.sessions.length === 0 ? (
          <p className="t3 small">No sessions in the last 30 days.</p>
        ) : (
          <>
            {r.playtime_seconds != null && <p className="small t2" style={{ marginBottom: 8 }}>{fmtDur(r.playtime_seconds * 1000, 2)} in the last 30 days.</p>}
            <table className="t">
              <tbody>
                {r.sessions.map((x) => {
                  const a = toMs(x.started_at) ?? 0
                  const b = x.ended_at ? toMs(x.ended_at) : null
                  return (
                    <tr key={x.id}>
                      <td>{fmtDay(a)}</td>
                      <td className="t2 num">
                        {fmtClock(a)} to {b ? fmtClock(b) : 'now'}
                      </td>
                      <td className="r num t2">{fmtDur((b ?? Date.now()) - a)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </>
        )
      }
    </Loaded>
  )
}

function Stats({ d }: { d: PlayerDetail }) {
  const s = d.stats
  if (!s) return <p className="t3 small">This server doesn’t keep stats for them yet.</p>
  return (
    <dl className={cx('facts')}>
      <dt>Deaths</dt>
      <dd className="num">{fmtNum(s.deaths ?? 0)}</dd>
      <dt>Mobs killed</dt>
      <dd className="num">{fmtNum(s.mob_kills ?? 0)}</dd>
      <dt>Players killed</dt>
      <dd className="num">{fmtNum(s.player_kills ?? 0)}</dd>
      <dt>Walked</dt>
      <dd className="num">{s.walked_cm != null ? `${fmtNum(s.walked_cm / 100000)} km` : '–'}</dd>
      <dt>Jumped</dt>
      <dd className="num">{s.jumps != null ? `${fmtNum(s.jumps)} times` : '–'}</dd>
      <dt>Score</dt>
      <dd className="num">{fmtNum(d.score)}</dd>
    </dl>
  )
}

export const _testing = { playedByName }
