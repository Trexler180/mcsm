import { useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { FleetServer, MyServerPermissions, Server } from '@/lib/types'
import { useAuthStore } from '@/store/auth'
import { groupFleet, placeServer, useFleet, useFolders } from '@/shell/fleet'
import { useGo } from '@/shell/nav'
import { paths } from '@/shell/paths'
import { Btn, Copy, Meter, StatePill, Tabs, type StatusKey } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtDur, relTime } from '@/ui/format'
import { MenuButton, MenuItem, MenuLabel, MenuSep } from '@/ui/overlays'
import { deniedTip } from '@/ui/perm'
import { ServerIcon } from '@/ui/pixel'
import { contentLabel, platformLabel } from '@/ui/platforms'
import { ask } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { plural } from '@/ui/util'
import { allowed, isBusyStatus, isLiveStatus, useIdentity } from './data'
import { SERVER_TABS, type ServerTab } from './tabs'

export function tabLabel(platform: string, tab: ServerTab): string {
  return tab === 'mods' ? contentLabel(platform) : SERVER_TABS.find((t) => t.value === tab)?.label ?? tab
}

export function ServerHeader({
  server,
  live,
  status,
  nodeName,
  perms,
  tab,
  crashedAt,
}: {
  server: Server
  live: FleetServer | undefined
  status: StatusKey
  nodeName: string
  perms: MyServerPermissions | undefined
  tab: ServerTab
  crashedAt?: number
}) {
  const go = useGo()
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  const identity = useIdentity(server.id).data
  const since = live?.online_since ?? server.online_since
  const label =
    status === 'online' && since
      ? 'Running for ' + fmtDur(Date.now() - since * 1000, 1)
      : (status === 'crashed' || status === 'startup_failure') && crashedAt
        ? (status === 'crashed' ? 'Crashed ' : 'Failed to start ') + relTime(crashedAt)
        : undefined
  const players = status === 'online' ? live?.players : null
  return (
    <header className="srv-head">
      <div className="srv-top">
        <ServerIcon id={server.id} icon={identity?.icon} size={40} className="srv-icon" />
        <div className="grow">
          <h1 className="srv-name ellipsis">{server.name}</h1>
          <div className="srv-facts">
            <StatePill status={status} label={label} />
            <span className="chip">
              {platformLabel(server.platform)} {server.mc_version}
            </span>
            {admin ? (
              <button type="button" className="chip" onClick={() => go(paths.nodes)}>
                {nodeName || 'Node'}
              </button>
            ) : (
              nodeName && <span className="chip">{nodeName}</span>
            )}
            {identity?.address && (
              <span className="chip">
                <Copy text={identity.address} mono />
              </span>
            )}
          </div>
        </div>
        <div className="srv-actions">
          <PowerButtons server={server} status={status} nodeName={nodeName} live={live} />
        </div>
      </div>
      <Tabs
        value={tab}
        onChange={(v) => go(paths.server(server.id, v))}
        label={`${server.name} pages`}
        items={SERVER_TABS.map((t) => {
          const ok = allowed(perms, t.perm)
          return {
            value: t.value,
            label: tabLabel(server.platform, t.value),
            disabled: !ok,
            tip: ok ? undefined : deniedTip(t.perm),
            count: t.value === 'players' && players ? players : undefined,
          }
        })}
      />
      <ServerProgress live={live} />
    </header>
  )
}

/** Start, stop, restart and the rest, for the state the server is in. */
function PowerButtons({ server, status, nodeName, live }: { server: Server; status: StatusKey; nodeName: string; live: FleetServer | undefined }) {
  const qc = useQueryClient()
  const name = server.name
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['server', server.id] })
    void qc.invalidateQueries({ queryKey: ['fleet'] })
  }
  const start = () => void act(`start ${name}`, () => api.servers.start(server.id)).then((r) => r !== undefined && refresh())
  const stop = () => void act(`stop ${name}`, () => api.servers.stop(server.id)).then((r) => r !== undefined && refresh())
  const restart = () => void act(`restart ${name}`, () => api.servers.restart(server.id)).then((r) => r !== undefined && refresh())
  const kill = async (force = false) => {
    const players = live?.players ?? 0
    const ok = await ask({
      title: `Kill ${name}?`,
      body: `It stops immediately, without saving.${players ? ` ${plural(players, 'player')} will be disconnected.` : ''} Use Stop unless it’s frozen.`,
      confirm: 'Kill',
      danger: true,
    })
    if (!ok) return
    const r = await act(`kill ${name}`, () => api.servers.kill(server.id, force ? { force: true } : undefined))
    if (r !== undefined) {
      toast('warn', `Killed ${name}`, 'Anything since the last save is gone.')
      refresh()
    }
  }
  const more = (o: { save?: boolean; killLabel?: string; force?: boolean }) => (
    <MenuButton
      label="More"
      variant=""
      size=""
      width={230}
      items={(close) => (
        <>
          {o.save && (
            <>
              <MenuItem
                close={close}
                need="console"
                onClick={() => void act('save the world', () => api.servers.command(server.id, 'save-all')).then((r) => r !== undefined && toast('ok', 'Saving the world'))}
              >
                Save the world now
              </MenuItem>
              <MenuSep />
            </>
          )}
          <MoveToFolderItems server={server} close={close} />
          {o.killLabel && (
            <>
              <MenuSep />
              <MenuItem danger close={close} need="power.kill" onClick={() => void kill(o.force)}>
                {o.killLabel}
              </MenuItem>
            </>
          )}
        </>
      )}
    />
  )
  if (status === 'unreachable') {
    return (
      <>
        <Btn disabled tip={`${nodeName || 'The node'} is offline`}>
          Start
        </Btn>
        {more({})}
      </>
    )
  }
  if (isBusyStatus(status)) {
    return (
      <>
        <Btn loading>{status === 'starting' ? 'Starting' : 'Stopping'}</Btn>
        {more({ killLabel: 'Kill now', force: true })}
      </>
    )
  }
  if (!isLiveStatus(status)) {
    return (
      <>
        <Btn variant="primary" need="power.start" onClick={start}>
          Start
        </Btn>
        {more({})}
      </>
    )
  }
  return (
    <>
      <Btn need="power.restart" onClick={restart}>
        Restart
      </Btn>
      <Btn need="power.stop" onClick={stop}>
        Stop
      </Btn>
      {more({ save: true, killLabel: 'Kill' })}
    </>
  )
}

/** "Move to folder" entries for a server's menu, ticking where it is now. */
function MoveToFolderItems({ server, close }: { server: Server; close: () => void }) {
  const qc = useQueryClient()
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  const fleet = useFleet().data
  const folders = useFolders().data ?? []
  if (!admin || !fleet) return null
  const here = server.folder_id && folders.some((f) => f.id === server.folder_id) ? server.folder_id : null
  const move = async (fid: string | null) => {
    if (fid === here) return
    const groups = groupFleet(fleet.servers, folders)
    const target = groups.find((g) => (g.folder?.id ?? null) === fid)
    const placement = placeServer(fleet, folders, server.id, fid, target?.servers.length ?? 0)
    const res = await act(`move ${server.name}`, () => api.servers.place(placement))
    if (!res) return
    void qc.invalidateQueries({ queryKey: ['fleet'] })
    void qc.invalidateQueries({ queryKey: ['server', server.id] })
    const name = (id: string | null) => folders.find((f) => f.id === id)?.name ?? 'Other'
    const was = res.previous.filter((p) => p.id === server.id)
    toast('ok', fid ? `Moved ${server.name} to ${name(fid)}` : `Took ${server.name} out of ${name(here)}`, undefined, {
      actions: [
        {
          label: 'Undo',
          run: async () => {
            await act(`move ${server.name} back`, () => api.servers.place(was))
            void qc.invalidateQueries({ queryKey: ['fleet'] })
            void qc.invalidateQueries({ queryKey: ['server', server.id] })
          },
        },
      ],
    })
  }
  return (
    <>
      <MenuLabel>Move to folder</MenuLabel>
      {folders.map((f) => (
        <MenuItem key={f.id} icon={here === f.id ? 'check' : null} close={close} onClick={() => void move(f.id)}>
          {f.name}
        </MenuItem>
      ))}
      <MenuItem icon={here === null ? 'check' : null} close={close} onClick={() => void move(null)}>
        No folder
      </MenuItem>
    </>
  )
}

/** Long jobs show here, whichever page started them. */
function ServerProgress({ live }: { live: FleetServer | undefined }) {
  const op = live?.operation
  if (!op || op.status !== 'running') return null
  const pct = op.progress != null ? op.progress * 100 : 0
  return (
    <div className="progress-banner" style={{ margin: '14px 0 0' }} role="status">
      <span className="grow">{operationText(op.kind, op.message, op.phase)}</span>
      <span className="track">
        <Meter value={pct} max={100} busy={op.progress == null} label="Progress" />
      </span>
    </div>
  )
}

const OPERATION_TEXT: Record<string, string> = {
  start: 'Starting',
  reinstall: 'Reinstalling',
  restore: 'Restoring a backup. It stays stopped until it’s done',
  backup: 'Backing up',
  migrate: 'Changing version',
  auto_update: 'Updating mods safely',
  fix: 'Fixing what stopped it starting',
  world_upload: 'Loading the new world',
}

function operationText(kind: string, message: string, phase: string): string {
  const what = OPERATION_TEXT[kind] ?? 'Working'
  const detail = message || phase
  return detail ? `${what}: ${detail.charAt(0).toLowerCase() + detail.slice(1)}.` : `${what}.`
}
