import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useRebootTracker } from '@/lib/reboot-tracker'
import type { FleetServer, Node } from '@/lib/types'
import { useAuthStore } from '@/store/auth'
import { effStatus, useFleet } from '@/shell/fleet'
import { useGo } from '@/shell/nav'
import { paths } from '@/shell/paths'
import { Btn, Check, Empty, Field, Status } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtClock, fmtDur, fmtMB, fmtNum, relTime } from '@/ui/format'
import { Dialog, MenuButton, MenuItem, MenuSep } from '@/ui/overlays'
import { PageHead } from '@/ui/page'
import { Loaded, SkItems } from '@/ui/skeleton'
import { ask, stepUp } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { copyText, plural, toMs } from '@/ui/util'

// The machines servers run on, each running the MCSM agent: how busy they
// are, what's on them, and rebooting, editing or removing one.

export function nodeAddress(n: Pick<Node, 'scheme' | 'fqdn' | 'port'>): string {
  const host = n.fqdn.includes(':') ? `[${n.fqdn}]` : n.fqdn
  return `${n.scheme}://${host}:${n.port}`
}

/** Memory promised to a node's servers, when it's more than the node has to give. */
export function overPromised(node: Pick<Node, 'memory_mb'>, servers: Pick<FleetServer, 'ram_limit_mb'>[]): number | null {
  const promised = servers.reduce((n, s) => n + (s.ram_limit_mb || 0), 0)
  return node.memory_mb && promised > node.memory_mb * 0.9 ? promised : null
}

export function NodesPage() {
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: ({ signal }) => api.nodes.list(signal), refetchInterval: 30_000 })
  const fleet = useFleet().data
  const [editing, setEditing] = useState<Node | 'new' | null>(null)
  return (
    <div className="scroll">
      <div className="page">
        <PageHead title="Nodes" lede="The machines your servers run on. Each one runs the MCSM agent.">
          {admin && <Btn onClick={() => setEditing('new')}>Add a node</Btn>}
        </PageHead>
        <Loaded query={nodes} sk={<SkItems rows={2} />} what="the nodes">
          {(list) =>
            list.length === 0 ? (
              <Empty title="No nodes yet." action={admin ? <Btn onClick={() => setEditing('new')}>Add a node</Btn> : undefined}>
                Install the agent on a machine, then add it here.
              </Empty>
            ) : (
              <div className="items flush node-list">
                {list.map((n) => (
                  <NodeRow key={n.id} n={n} admin={admin} servers={(fleet?.servers ?? []).filter((s) => s.node_id === n.id)} fleetNodes={fleet?.nodes} onEdit={() => setEditing(n)} />
                ))}
              </div>
            )
          }
        </Loaded>
      </div>
      {editing && <NodeDialog node={editing === 'new' ? null : editing} onClose={() => setEditing(null)} />}
    </div>
  )
}

function NodeRow({ n, admin, servers, fleetNodes, onEdit }: { n: Node; admin: boolean; servers: FleetServer[]; fleetNodes: Parameters<typeof effStatus>[1]; onEdit: () => void }) {
  const qc = useQueryClient()
  const go = useGo()
  const startTracking = useRebootTracker((s) => s.start)
  const running = servers.filter((s) => s.status === 'online' || s.status === 'starting')
  const promised = overPromised(n, servers)
  const seen = toMs(n.last_seen)

  const reboot = async () => {
    const v = await stepUp({
      title: `Reboot ${n.name}?`,
      body:
        `${running.length ? `${plural(running.length, 'running server')} ${running.length === 1 ? 'is' : 'are'} stopped cleanly first, and players are disconnected.` : 'Nothing is running on it.'} ` +
        'If the panel runs on this machine too, it goes away until the machine is back; this page waits and comes back by itself. ' +
        'Servers set to start automatically come back with the panel.',
      confirm: 'Reboot',
    })
    if (!v) return
    const r = await act(`reboot ${n.name}`, () => api.nodes.reboot(n.id, { password: v.password, totp_code: v.code || undefined }))
    if (r === undefined) return
    startTracking(n, r.requested_at)
    void qc.invalidateQueries({ queryKey: ['nodes'] })
  }

  const remove = async () => {
    if (!(await ask({ title: `Remove ${n.name}?`, body: 'The panel forgets it and stops talking to its agent. Nothing on the machine is deleted.', confirm: 'Remove', danger: true }))) return
    const r = await act(`remove ${n.name}`, () => api.nodes.delete(n.id).then(() => true))
    if (!r) return
    toast('ok', `Removed ${n.name}`)
    void qc.invalidateQueries({ queryKey: ['nodes'] })
    void qc.invalidateQueries({ queryKey: ['fleet'] })
  }

  return (
    <div className="item node-row">
      <div className="row" style={{ gap: 12, alignItems: 'baseline', flexWrap: 'wrap' }}>
        <h2 className="section-title" style={{ fontSize: 15 }}>
          {n.name}
        </h2>
        {n.location && <span className="t3">{n.location}</span>}
        <span className="t3 mono small">{nodeAddress(n)}</span>
        <span className="grow" />
        {n.online && admin && (
          <Btn size="sm" onClick={() => void reboot()}>
            Reboot
          </Btn>
        )}
        {admin && (
          <MenuButton
            label={`More for ${n.name}`}
            items={(close) => (
              <>
                <MenuItem close={close} onClick={onEdit}>
                  Edit
                </MenuItem>
                <MenuItem
                  close={close}
                  onClick={() => {
                    void copyText(nodeAddress(n))
                    toast('ok', `Copied ${nodeAddress(n)}`)
                  }}
                >
                  Copy its address
                </MenuItem>
                <MenuSep />
                <MenuItem danger close={close} disabled={servers.length > 0} onClick={() => void remove()}>
                  {servers.length ? 'Remove (move or delete its servers first)' : 'Remove'}
                </MenuItem>
              </>
            )}
          />
        )}
      </div>
      {n.online ? (
        <dl className="facts node-facts">
          <dt>CPU</dt>
          <dd className="num">
            {n.cpu_pct != null ? `${Math.round(n.cpu_pct)}%` : '–'}
            {n.cpu_cores ? ` of ${n.cpu_cores} cores` : ''}
          </dd>
          <dt>Up</dt>
          <dd>{n.uptime_seconds != null ? fmtDur(n.uptime_seconds * 1000, 1) : '–'}</dd>
          <dt>Memory</dt>
          <dd className="num">
            {n.mem_used_mb != null ? `${fmtMB(n.mem_used_mb, 1)} of ` : ''}
            {fmtMB(n.memory_mb)} in use
            {promised && <span className="warn">, {fmtMB(promised)} promised to servers</span>}
          </dd>
          <dt>System</dt>
          <dd>{[n.os, n.arch].filter(Boolean).join(', ') || '–'}</dd>
          <dt>Disk</dt>
          <dd className="num">
            {n.disk_used_gb != null && n.disk_gb ? `${fmtNum(n.disk_used_gb)} of ${fmtNum(n.disk_gb)} GB` : n.disk_gb ? `${fmtNum(n.disk_gb)} GB` : '–'}
          </dd>
          <dt>Agent</dt>
          <dd>
            {n.agent_version ? <span className="mono">{n.agent_version.slice(0, 12)}</span> : '–'}
            {n.isolation && !n.isolation.active && (
              <span className={n.isolation.mode === 'off' ? 't3' : 'warn'} data-tip={n.isolation.reason || undefined}>
                {n.isolation.mode === 'off' ? ', servers not isolated (turned off)' : n.isolation.mode === 'enforced' ? ', isolation required but unavailable: servers won’t start' : ', servers not isolated'}
              </span>
            )}
            {n.isolation?.active && <span className="t3">, servers run as {n.isolation.user}</span>}
          </dd>
        </dl>
      ) : (
        <p className="t2" style={{ marginTop: 10, maxWidth: '70ch' }}>
          <span className="bad">{seen ? `Offline since ${fmtClock(seen)} (${relTime(seen)}).` : 'The panel has never reached it.'}</span> Check that the machine is on and the agent is running:{' '}
          <code>systemctl status mcsm-agent</code>
        </p>
      )}
      {servers.length > 0 && (
        <div className="row wrap" style={{ gap: '4px 18px', marginTop: 14 }}>
          {servers.map((s) => (
            <button key={s.id} type="button" className="row small" style={{ gap: 7 }} onClick={() => go(paths.server(s.id))}>
              <Status status={effStatus(s, fleetNodes)} quiet />
              <span className="link">{s.name}</span>
              <span className="t3">{fmtMB(s.ram_limit_mb)}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

/** Where the agent listens, in pieces the dialog edits. */
export function nodeFormError(f: { name: string; fqdn: string; port: string; token: string }, needToken: boolean): Partial<Record<'name' | 'fqdn' | 'port' | 'token', string>> {
  const e: Partial<Record<'name' | 'fqdn' | 'port' | 'token', string>> = {}
  if (!f.name.trim()) e.name = 'Give it a name.'
  if (!f.fqdn.trim()) e.fqdn = 'Enter its hostname or IP address.'
  else if (/[/\s]|^\w+:\/\//.test(f.fqdn.trim())) e.fqdn = 'Just the host, without http:// or a path.'
  const port = Number(f.port)
  if (!Number.isInteger(port) || port < 1 || port > 65535) e.port = 'A port is 1 to 65535.'
  if (needToken && !f.token.trim()) e.token = 'Paste the token from the agent’s config.'
  return e
}

function NodeDialog({ node, onClose }: { node: Node | null; onClose: () => void }) {
  const qc = useQueryClient()
  const [f, setF] = useState({
    name: node?.name ?? '',
    fqdn: node?.fqdn ?? '',
    port: String(node?.port ?? 8090),
    location: node?.location ?? '',
    token: '',
    https: node?.scheme === 'https',
  })
  const [touched, setTouched] = useState(false)
  const [busy, setBusy] = useState(false)
  // A new node always needs its token; an existing one only when it moves.
  const moved = !!node && (f.fqdn.trim() !== node.fqdn || Number(f.port) !== node.port || (f.https ? 'https' : 'http') !== node.scheme)
  const errors = nodeFormError(f, !node || moved)
  const ok = Object.keys(errors).length === 0
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => setF((p) => ({ ...p, [k]: e.target.value }))
  const save = async () => {
    setTouched(true)
    if (!ok) return
    setBusy(true)
    const body = { name: f.name.trim(), fqdn: f.fqdn.trim(), port: Number(f.port), scheme: f.https ? 'https' : 'http', location: f.location.trim() || null, ...(f.token.trim() ? { token: f.token.trim() } : {}) }
    const r = await act(node ? `save ${node.name}` : `add ${body.name}`, () => (node ? api.nodes.update(node.id, body) : api.nodes.create({ ...body, token: f.token.trim() })))
    setBusy(false)
    if (r === undefined) return
    toast('ok', node ? `Saved ${body.name}` : `Added ${body.name}`, node ? undefined : 'It shows as online once the panel reaches its agent.')
    void qc.invalidateQueries({ queryKey: ['nodes'] })
    void qc.invalidateQueries({ queryKey: ['fleet'] })
    onClose()
  }
  const err = (k: keyof typeof errors) => (touched ? errors[k] : undefined)
  return (
    <Dialog
      open
      onClose={onClose}
      title={node ? `Edit ${node.name}` : 'Add a node'}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" loading={busy} onClick={() => void save()}>
            {node ? 'Save' : 'Add node'}
          </Btn>
        </>
      }
    >
      <form
        className="stack"
        style={{ gap: 14 }}
        onSubmit={(e) => {
          e.preventDefault()
          void save()
        }}
      >
        {!node && <p className="t2">Install the MCSM agent on the machine first. Then enter where it listens and the token from its configuration.</p>}
        <div className="form-grid">
          <Field label="Name" error={err('name')}>
            <input className="input" value={f.name} onChange={set('name')} placeholder="basement-box" autoFocus aria-label="Name" />
          </Field>
          <Field label="Location" hint="Optional, for your own reference.">
            <input className="input" value={f.location} onChange={set('location')} placeholder="Home office" aria-label="Location" />
          </Field>
          <Field label="Hostname or IP" error={err('fqdn')}>
            <input className="input mono" value={f.fqdn} onChange={set('fqdn')} placeholder="node1.example.net" aria-label="Hostname or IP" />
          </Field>
          <Field label="Agent port" error={err('port')}>
            <input className="input num" inputMode="numeric" value={f.port} onChange={set('port')} aria-label="Agent port" />
          </Field>
          <Field label="Agent token" className="span" error={err('token')} hint={node ? (moved ? 'Needed because the address changed.' : 'Leave empty to keep the current one.') : undefined}>
            <input className="input mono" type="password" value={f.token} onChange={set('token')} autoComplete="off" aria-label="Agent token" />
          </Field>
        </div>
        <Check checked={f.https} onChange={(v) => setF((p) => ({ ...p, https: v }))} label="The agent uses HTTPS" />
        <button type="submit" hidden />
      </form>
    </Dialog>
  )
}
