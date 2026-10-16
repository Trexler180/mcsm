import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { InstalledMod, ModImpact, ModProjectType, ModSearchHit, ModSortIndex, ModSource, ModUpdate, ModVersion, MyServerPermissions, Server, SkippedModVersion } from '@/lib/types'
import { ServerPermissionsProvider } from '@/lib/server-permissions'
import { Btn, Check, Choices, Empty, Field, Notice, Search, Select, Switch, Tabs, TextBtn, type StatusKey } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtCompact, fmtDate, fmtNum, relTime } from '@/ui/format'
import { Dialog, MenuButton, MenuItem, MenuSep, Sheet } from '@/ui/overlays'
import { useCan } from '@/ui/perm'
import { PLATFORMS, contentLabel, platformLabel } from '@/ui/platforms'
import { Loaded, SheetSkeleton, SkForm, SkItems, SkTable } from '@/ui/skeleton'
import { toast } from '@/ui/toasts'
import { cx, plural, toMs } from '@/ui/util'
import { allowed } from './data'
import { InstalledModIcon, ModIcon } from './mod-icon'

// Mods, plugins or datapacks: what's installed and how to change it, finding
// more, and their config files. Changes load the next time the server starts.

const ConfigsTab = lazy(() => import('@/components/configs/configs-tab').then((m) => ({ default: m.ConfigsTab })))

export type ModsView = 'installed' | 'browse' | 'configs'

export function modsViewOf(sub: string | undefined): ModsView {
  return sub === 'browse' || sub === 'configs' ? sub : 'installed'
}

export function ModsTab({ server, status, perms, view, setView }: { server: Server; status: StatusKey; perms: MyServerPermissions | undefined; view: ModsView; setView: (v: ModsView) => void }) {
  const qc = useQueryClient()
  const mods = useQuery({ queryKey: ['mods', server.id], queryFn: () => api.mods.list(server.id) })
  const updates = useQuery({ queryKey: ['mod-updates', server.id], queryFn: () => api.mods.updates(server.id), staleTime: 5 * 60_000, retry: false }).data ?? []
  const [safe, setSafe] = useState(false)
  const [pct, setPct] = useState<number | null>(null)
  const upRef = useRef<HTMLInputElement>(null)
  const word = contentLabel(server.platform)
  const pending = updates.filter((u) => u.enabled && !mods.data?.find((m) => m.id === u.mod_id)?.pinned)
  const upload = async (files: File[]) => {
    const bad = files.find((f) => !/\.(jar|zip)$/i.test(f.name))
    if (bad) {
      toast('bad', `${bad.name} isn’t a .jar or .zip`)
      return
    }
    setPct(0)
    const r = await act(files.length === 1 ? `upload ${files[0].name}` : `upload ${files.length} files`, () =>
      api.mods.uploadCustom(server.id, files, (p) => setPct(p.total ? Math.round((p.loaded / p.total) * 100) : 0)),
    )
    setPct(null)
    if (r === undefined) return
    toast('ok', files.length === 1 ? `Uploaded ${files[0].name}` : `Uploaded ${files.length} files`, status === 'online' ? 'Restart the server to load it.' : undefined)
    void qc.invalidateQueries({ queryKey: ['mods', server.id] })
  }
  return (
    <>
      <div className="toolbar tabbed">
        <Tabs
          sub
          value={view}
          onChange={setView}
          label={word}
          items={[
            { value: 'installed', label: 'Installed', count: mods.data?.length },
            { value: 'browse', label: 'Browse' },
            { value: 'configs', label: 'Configs', disabled: !allowed(perms, 'files') },
          ]}
        />
        <span className="grow" />
        <input
          ref={upRef}
          type="file"
          accept=".jar,.zip"
          multiple
          hidden
          onChange={(e) => {
            const files = Array.from(e.target.files ?? [])
            e.target.value = ''
            if (files.length) void upload(files)
          }}
        />
        <Btn need="mods.install" loading={pct !== null} onClick={() => upRef.current?.click()}>
          {pct !== null ? `Uploading ${pct}%` : 'Upload .jar'}
        </Btn>
        {pending.length > 0 && (
          <Btn variant="primary" need="mods.update" onClick={() => setSafe(true)}>
            Update {pending.length}
          </Btn>
        )}
      </div>
      {view === 'installed' && (
        <Loaded query={mods} sk={<SkTable rows={8} icons />} what={`${word.toLowerCase()} on ${server.name}`}>
          {(list) => <Installed server={server} status={status} list={list} updates={updates} />}
        </Loaded>
      )}
      {view === 'browse' && <Browse server={server} status={status} installed={mods.data ?? []} />}
      {view === 'configs' && (
        <ServerPermissionsProvider permissions={perms}>
          <Suspense fallback={<SkForm />}>
            <div className="legacy-page" style={{ overflow: 'visible', minHeight: 480 }}>
              <ConfigsTab serverId={server.id} />
            </div>
          </Suspense>
        </ServerPermissionsProvider>
      )}
      {safe && <SafeUpdateDialog server={server} status={status} updates={pending} onClose={() => setSafe(false)} />}
    </>
  )
}

// ---- what's installed ---------------------------------------------------------------------------

function note(m: InstalledMod): { text: string; warn?: boolean } {
  if (m.orphaned) return { text: 'Installed as a dependency, and nothing needs it now. Safe to remove.', warn: true }
  if (m.required_by.length) return { text: 'Needed by ' + m.required_by.join(', ') }
  if (m.source === 'upload' || !m.source_id) return { text: 'Uploaded by hand. No update checks.' }
  if (m.installed_as_dep) return { text: 'Installed as a dependency.' }
  return { text: m.file_name }
}

function projectUrl(m: InstalledMod): string | null {
  if (!m.source_id) return null
  if (m.source === 'modrinth') return `https://modrinth.com/project/${m.source_id}`
  if (m.source === 'hangar') return `https://hangar.papermc.io/${m.source_id}`
  if (m.source === 'spigotmc') return `https://www.spigotmc.org/resources/${m.source_id}`
  return null
}

function Installed({ server, status, list, updates }: { server: Server; status: StatusKey; list: InstalledMod[]; updates: ModUpdate[] }) {
  const qc = useQueryClient()
  const [q, setQ] = useState('')
  const [impact, setImpact] = useState<{ mod: InstalledMod; mode: 'uninstall' | 'disable' } | null>(null)
  const [versions, setVersions] = useState<InstalledMod | null>(null)
  const [checking, setChecking] = useState(false)
  const skipped = useQuery({ queryKey: ['mod-skipped', server.id], queryFn: () => api.mods.skippedVersions(server.id), staleTime: 60_000, retry: false }).data ?? []
  const word = contentLabel(server.platform).toLowerCase()
  const updateOf = (m: InstalledMod) => updates.find((u) => u.mod_id === m.id)
  const skippedOf = (m: InstalledMod) => skipped.filter((v) => v.project_id === m.source_id)
  const rows = list
    .filter((m) => !q || m.name.toLowerCase().includes(q.toLowerCase()))
    .sort((a, b) => Number(!!updateOf(b) && !b.pinned) - Number(!!updateOf(a) && !a.pinned) || a.name.localeCompare(b.name))
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['mods', server.id] })
    void qc.invalidateQueries({ queryKey: ['mod-updates', server.id] })
    void qc.invalidateQueries({ queryKey: ['mod-skipped', server.id] })
    void qc.invalidateQueries({ queryKey: ['fleet'] })
  }
  const unskip = (m: InstalledMod, v: SkippedModVersion) =>
    void act(`offer ${m.name} ${v.version} again`, () => api.mods.unskipVersion(server.id, v.project_id, v.version_id).then(() => true)).then((r) => {
      if (r === undefined) return
      toast('ok', `${m.name} ${v.version} is offered again`)
      refresh()
    })
  const later = status === 'online' ? 'It loads after a restart.' : undefined
  const toggle = (m: InstalledMod, on: boolean) => {
    if (!on && m.required_by.length) {
      setImpact({ mod: m, mode: 'disable' })
      return
    }
    void act(on ? `turn on ${m.name}` : `turn off ${m.name}`, () => api.mods.setEnabled(server.id, m.id, on)).then((r) => {
      if (r === undefined) return
      toast('ok', on ? `Turned on ${m.name}` : `Turned off ${m.name}`, later)
      refresh()
    })
  }
  const update = (m: InstalledMod, u: ModUpdate) =>
    void act(`update ${m.name}`, () => api.mods.update(server.id, m.id, u.latest_version_id)).then((r) => {
      if (r === undefined) return
      toast('ok', `${m.name} is ${u.latest_version} now`, later)
      refresh()
    })
  const pin = (m: InstalledMod) =>
    void act(m.pinned ? `unpin ${m.name}` : `pin ${m.name}`, () => api.mods.pin(server.id, m.id, !m.pinned)).then((r) => {
      if (r === undefined) return
      toast('ok', m.pinned ? `Unpinned ${m.name}` : `Pinned ${m.name} at ${m.version}`, m.pinned ? undefined : 'Updates leave it alone.')
      refresh()
    })
  const check = async () => {
    setChecking(true)
    const r = await act('check for updates', async () => {
      await api.mods.rescan(server.id)
      return api.mods.updates(server.id)
    })
    setChecking(false)
    if (r === undefined) return
    qc.setQueryData(['mod-updates', server.id], r)
    void qc.invalidateQueries({ queryKey: ['mods', server.id] })
    toast('ok', 'Checked for updates', r.length ? plural(r.length, 'update') + ' available.' : 'Everything is current.')
  }
  return (
    <>
      <div className="toolbar">
        <Search value={q} onChange={setQ} placeholder={`Filter ${word}`} size="sm" style={{ width: 240 }} />
        <span className="grow" />
        <TextBtn quiet onClick={() => void check()}>
          {checking ? 'Checking…' : 'Check for updates'}
        </TextBtn>
      </div>
      {rows.length === 0 ? (
        <Empty title={list.length ? 'Nothing matches.' : `No ${word} yet.`}>{!list.length && 'Find some under Browse, or upload a jar.'}</Empty>
      ) : (
        <table className="t mods-t">
          <thead>
            <tr>
              <th>Name</th>
              <th style={{ width: 170 }}>Version</th>
              <th style={{ width: 150 }}>Update</th>
              <th className="c" style={{ width: 56 }}>
                On
              </th>
              <th style={{ width: 48 }} />
            </tr>
          </thead>
          <tbody>
            {rows.map((m) => {
              const u = updateOf(m)
              const n = note(m)
              return (
                <tr key={m.id} className={m.enabled ? undefined : 'muted'}>
                  <td className="mod-name">
                    <div className="row" style={{ gap: 12 }}>
                      <InstalledModIcon serverId={server.id} source={m.source} sourceId={m.source_id} name={m.name} size={32} style={m.enabled ? undefined : { opacity: 0.4, filter: 'grayscale(1)' }} />
                      <div className="grow">
                        <div className="strong ellipsis" style={{ color: m.enabled ? undefined : 'var(--text-3)' }}>
                          {m.name}
                        </div>
                        <div className={cx('sub ellipsis', n.warn && 'warn')}>{n.text}</div>
                        {skippedOf(m).map((v) => (
                          <div key={v.version_id} className="sub warn">
                            <span data-tip={v.reason || undefined}>{v.version} is skipped: it stopped the server starting.</span>{' '}
                            <TextBtn quiet need="mods.update" onClick={() => unskip(m, v)}>
                              Offer it again
                            </TextBtn>
                          </div>
                        ))}
                      </div>
                    </div>
                  </td>
                  <td className="t2 num nowrap mod-ver">
                    {m.version === 'custom' || m.version === 'unknown' ? <span className="t3">–</span> : m.version}
                    {m.pinned && (
                      <span className="t3" data-tip="Pinned. Updates leave it alone.">
                        {' '}
                        pinned
                      </span>
                    )}
                  </td>
                  <td className="nowrap">
                    {u && !m.pinned && (
                      <Btn size="sm" className="upd" need="mods.update" tip={`Update to ${u.latest_version}`} aria-label={`Update ${m.name} to ${u.latest_version}`} onClick={() => update(m, u)}>
                        <span aria-hidden="true">→</span> {u.latest_version}
                      </Btn>
                    )}
                  </td>
                  <td className="c">
                    <Switch checked={m.enabled} label={`${m.name} on`} need="mods.update" onChange={(v) => toggle(m, v)} />
                  </td>
                  <td className="actions">
                    <MenuButton
                      label={`More for ${m.name}`}
                      items={(close) => (
                        <>
                          <MenuItem close={close} need="mods.update" disabled={!m.source_id} onClick={() => setVersions(m)}>
                            Other versions
                          </MenuItem>
                          <MenuItem close={close} need="mods.update" onClick={() => pin(m)}>
                            {m.pinned ? 'Unpin' : 'Pin this version'}
                          </MenuItem>
                          {projectUrl(m) && (
                            <MenuItem close={close} onClick={() => window.open(projectUrl(m) as string, '_blank', 'noopener')}>
                              Project page
                            </MenuItem>
                          )}
                          <MenuItem close={close} need="files.read" onClick={() => void act(`download ${m.file_name}`, () => api.files.download(server.id, m.install_path))}>
                            Download the jar
                          </MenuItem>
                          <MenuSep />
                          <MenuItem danger close={close} need="mods.remove" onClick={() => setImpact({ mod: m, mode: 'uninstall' })}>
                            Uninstall
                          </MenuItem>
                        </>
                      )}
                    />
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      {impact && <ImpactDialog server={server} status={status} target={impact} onClose={() => setImpact(null)} onDone={refresh} />}
      {versions && <VersionDialog server={server} status={status} mod={versions} onClose={() => setVersions(null)} onDone={refresh} />}
    </>
  )
}

function ImpactDialog({ server, status, target, onClose, onDone }: { server: Server; status: StatusKey; target: { mod: InstalledMod; mode: 'uninstall' | 'disable' }; onClose: () => void; onDone: () => void }) {
  const { mod, mode } = target
  const [cascade, setCascade] = useState(true)
  const [busy, setBusy] = useState(false)
  const impact = useQuery({ queryKey: ['mod-dependents', server.id, mod.id], queryFn: () => api.mods.dependents(server.id, mod.id), staleTime: 0, gcTime: 0 })
  const go = async (i: ModImpact | undefined) => {
    setBusy(true)
    const disableDependents = !!i?.required.length && cascade
    const r = await act(mode === 'uninstall' ? `uninstall ${mod.name}` : `turn off ${mod.name}`, async () => {
      if (mode === 'uninstall') {
        const res = await api.mods.uninstall(server.id, mod.id, { force: true, disableDependents })
        return { disabled: res?.disabled ?? [], failed: res?.failed ?? [] }
      }
      const res = await api.mods.setEnabled(server.id, mod.id, false, { force: true, disableDependents })
      return { disabled: res.dependents_disabled ?? [], failed: res.dependents_failed ?? [] }
    })
    setBusy(false)
    if (r === undefined) return
    toast('ok', mode === 'uninstall' ? `Uninstalled ${mod.name}` : `Turned off ${mod.name}`, r.disabled.length ? `${plural(r.disabled.length, 'other')} turned off as well.` : undefined)
    if (r.failed.length) toast('bad', 'Some still need it', `Couldn’t turn off ${r.failed.join(', ')}. Turn them off before restarting.`)
    onDone()
    onClose()
  }
  return (
    <Dialog
      open
      onClose={onClose}
      title={`${mode === 'uninstall' ? 'Uninstall' : 'Turn off'} ${mod.name}?`}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant={mode === 'uninstall' ? 'danger primary' : 'primary'} loading={busy} disabled={impact.isLoading} onClick={() => void go(impact.data)}>
            {mode === 'uninstall' ? 'Uninstall' : 'Turn off'}
          </Btn>
        </>
      }
    >
      <Loaded query={impact} sk={<SheetSkeleton />} what="what needs it">
        {(i) => (
          <div className="stack" style={{ gap: 14 }}>
            {i.required.length ? (
              <>
                <p>
                  {i.required.map((d) => d.name).join(' and ')} {i.required.length === 1 ? 'needs' : 'need'} it. Without it, {server.name} won’t start.
                </p>
                <Check checked={cascade} onChange={setCascade} label={`Turn off ${i.required.map((d) => d.name).join(' and ')} as well`} />
              </>
            ) : i.checked ? (
              <p className="t2">Nothing else on {server.name} needs it.</p>
            ) : null}
            {i.optional.length > 0 && <p className="small t2">{i.optional.map((d) => d.name).join(', ')} work with it when it’s there, and carry on without it.</p>}
            {mode === 'uninstall' && i.orphaning.length > 0 && (
              <p className="small t2">
                {i.orphaning.join(', ')} {i.orphaning.length === 1 ? 'was' : 'were'} only there for it; they show as safe to remove afterwards.
              </p>
            )}
            {!i.checked && <p className="warn">It was uploaded by hand, so the panel can’t tell whether anything needs it.</p>}
            {i.unchecked > 0 && <p className="small t3">{plural(i.unchecked, 'other uploaded jar')} can’t be checked.</p>}
            <p className="small t3">{status === 'online' ? 'This takes effect when the server next restarts.' : 'This takes effect the next time it starts.'}</p>
          </div>
        )}
      </Loaded>
    </Dialog>
  )
}

function VersionDialog({ server, status, mod, onClose, onDone }: { server: Server; status: StatusKey; mod: InstalledMod; onClose: () => void; onDone: () => void }) {
  const [sel, setSel] = useState<string>(mod.version_id ?? '')
  const [all, setAll] = useState(false)
  const [busy, setBusy] = useState(false)
  const versions = useQuery({
    queryKey: ['mod-versions', mod.source, server.id, mod.source_id, all ? '' : server.platform, all ? '' : server.mc_version],
    queryFn: () => api.mods.getVersions(server.id, mod.source_id as string, all ? undefined : server.platform, all ? undefined : server.mc_version, mod.source),
    staleTime: 10 * 60_000,
  })
  const install = async (list: ModVersion[]) => {
    const v = list.find((x) => x.id === sel)
    if (!v) return
    setBusy(true)
    const newest = list[0]?.id === v.id
    const r = await act(`switch ${mod.name} to ${v.version_number}`, async () => {
      await api.mods.update(server.id, mod.id, v.id)
      if (!newest && !mod.pinned) await api.mods.pin(server.id, mod.id, true)
      return true
    })
    setBusy(false)
    if (r === undefined) return
    toast('ok', `${mod.name} is ${v.version_number} now`, !newest ? 'Pinned, so updates leave it alone.' : status === 'online' ? 'It loads after a restart.' : undefined)
    onDone()
    onClose()
  }
  return (
    <Dialog
      open
      onClose={onClose}
      size="wide"
      title={`${mod.name} versions${all ? '' : ` for ${server.mc_version}`}`}
      foot={
        <>
          <Check checked={all} onChange={setAll} label="Show versions for other loaders and Minecraft versions" />
          <span className="grow" />
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" disabled={!sel || sel === mod.version_id} loading={busy} onClick={() => void install(versions.data ?? [])}>
            Install {versions.data?.find((v) => v.id === sel)?.version_number ?? ''}
          </Btn>
        </>
      }
    >
      <Loaded query={versions} sk={<SkItems rows={4} />} what="its versions">
        {(list) =>
          list.length === 0 ? (
            <Empty title="No versions match.">Show versions for other loaders to see everything it has.</Empty>
          ) : (
            <div style={{ maxHeight: 420, overflowY: 'auto' }}>
              <Choices
                value={sel}
                onChange={setSel}
                options={list.slice(0, 30).map((v) => ({
                  value: v.id,
                  label: (
                    <>
                      {v.version_number}
                      {v.id === mod.version_id && (
                        <span className="t3" style={{ fontWeight: 400 }}>
                          {' '}
                          (installed)
                        </span>
                      )}
                      {v.version_type && v.version_type !== 'release' && (
                        <span className="warn" style={{ fontWeight: 400 }}>
                          {' '}
                          {v.version_type}
                        </span>
                      )}
                    </>
                  ),
                  desc: `Released ${fmtDate(toMs(v.date_published) ?? 0, false)}, for ${gameVersions(v.game_versions, server.mc_version)}${v.loaders.length ? ` on ${v.loaders.join(', ')}` : ''}.`,
                }))}
              />
            </div>
          )
        }
      </Loaded>
    </Dialog>
  )
}

/** The Minecraft versions a release is for, short: the server's own when it's
 *  one of many. */
export function gameVersions(list: string[], mine: string): string {
  if (list.length <= 3) return list.join(', ') || 'any version'
  if (list.includes(mine)) return `${mine} and ${plural(list.length - 1, 'other')}`
  return `${list[0]} to ${list[list.length - 1]}`
}

function SafeUpdateDialog({ server, status, updates, onClose }: { server: Server; status: StatusKey; updates: ModUpdate[]; onClose: () => void }) {
  const qc = useQueryClient()
  const [backup, setBackup] = useState(true)
  const up = status === 'online'
  const start = () => {
    onClose()
    void act(`update ${plural(updates.length, 'mod')} on ${server.name}`, async () => {
      // Every job holds the server, so the update waits for the backup.
      if (backup) {
        toast('info', `Backing up ${server.name} first`)
        const b = await api.backups.create(server.id)
        await api.operations.wait(server.id, b.operation)
      }
      return api.mods.autoUpdate(server.id)
    }).then((r) => {
      if (r === undefined) return
      toast('info', `Updating ${plural(updates.length, 'mod')} on ${server.name}`, 'You’ll see how it went here and in Alerts.')
      void qc.invalidateQueries({ queryKey: ['fleet'] })
      void qc.invalidateQueries({ queryKey: ['mod-update-runs', server.id] })
    })
  }
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Update ${plural(updates.length, 'mod')} on ${server.name}`}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" onClick={start}>
            Update
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 14 }}>
        <table className="t">
          <tbody>
            {updates.map((u) => (
              <tr key={u.mod_id}>
                <td>{u.name}</td>
                <td className="r t2 num nowrap">
                  {u.current_version} to {u.latest_version}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <p className="t2">
          MCSM swaps the files, {up ? 'restarts the server' : 'starts the server once'} and watches it come up. If an update stops it from starting, that update is undone and skipped next time.
          {up ? ' Anyone online is disconnected for a minute.' : ''}
        </p>
        <Check checked={backup} onChange={setBackup} label="Back up first" />
      </div>
    </Dialog>
  )
}

// ---- finding more -----------------------------------------------------------------------------

const SORTS: { value: ModSortIndex; label: string }[] = [
  { value: 'relevance', label: 'Best match' },
  { value: 'downloads', label: 'Most downloaded' },
  { value: 'follows', label: 'Most followed' },
  { value: 'updated', label: 'Recently updated' },
  { value: 'newest', label: 'Newest' },
]
const SOURCE_LABEL: Record<ModSource, string> = { modrinth: 'Modrinth', curseforge: 'CurseForge', hangar: 'Hangar', spigotmc: 'SpigotMC' }

// Focusing a field on a phone opens its keyboard over the results.
const finePointer = () => typeof window !== 'undefined' && !!window.matchMedia?.('(pointer: fine)').matches

function kindsFor(platform: string): { value: ModProjectType; label: string }[] {
  const c = PLATFORMS[platform]?.content
  if (c === 'plugin') return [{ value: 'plugin', label: 'Plugins' }]
  if (c === 'datapack') return [{ value: 'datapack', label: 'Datapacks' }]
  return [
    { value: 'mod', label: 'Mods' },
    { value: 'modpack', label: 'Modpacks' },
    { value: 'datapack', label: 'Datapacks' },
  ]
}

function Browse({ server, status, installed }: { server: Server; status: StatusKey; installed: InstalledMod[] }) {
  const plugin = PLATFORMS[server.platform]?.content === 'plugin'
  const sources = useQuery({ queryKey: ['mod-sources', server.id], queryFn: () => api.mods.sources(server.id), staleTime: 60 * 60_000, retry: false }).data
  const kinds = kindsFor(server.platform)
  const [kind, setKind] = useState<ModProjectType>(kinds[0].value)
  const available: ModSource[] = (plugin ? (['hangar', 'modrinth', 'spigotmc'] as ModSource[]) : (['modrinth', 'curseforge'] as ModSource[])).filter((s) => !sources || sources[s] !== false)
  const [source, setSource] = useState<ModSource>(available[0] ?? 'modrinth')
  useEffect(() => {
    if (!available.includes(source) && available[0]) setSource(available[0])
  }, [available, source])
  const [q, setQ] = useState('')
  const [dq, setDq] = useState('')
  useEffect(() => {
    const t = setTimeout(() => setDq(q), 300)
    return () => clearTimeout(t)
  }, [q])
  const [cat, setCat] = useState('')
  const [sort, setSort] = useState<ModSortIndex>('relevance')
  const [fits, setFits] = useState(true)
  const [page, setPage] = useState(0)
  useEffect(() => setPage(0), [dq, cat, sort, fits, source, kind])
  const [detail, setDetail] = useState<ModSearchHit | null>(null)
  const [install, setInstall] = useState<ModSearchHit | null>(null)
  const categories = useQuery({ queryKey: ['mod-categories', server.id, kind, source], queryFn: () => api.mods.categories(server.id, kind, source), staleTime: 60 * 60_000, retry: false }).data ?? []
  const loader = kind === 'mod' || kind === 'plugin' || kind === 'modpack' ? server.platform : undefined
  const results = useQuery({
    queryKey: ['mod-search', server.id, source, kind, dq, cat, sort, fits, page],
    queryFn: () =>
      api.mods.search(server.id, {
        query: dq,
        source,
        loader: fits ? loader : undefined,
        mcVersion: fits ? server.mc_version : undefined,
        projectType: kind,
        categories: cat ? [cat] : undefined,
        index: sort === 'relevance' && !dq ? 'downloads' : sort,
        limit: 20,
        offset: page * 20,
      }),
    staleTime: 60_000,
    placeholderData: (prev) => prev,
  })
  const installedIds = new Set(installed.map((m) => m.source_id).filter(Boolean))
  const cats = categories.filter((c) => c.header === 'categories' || !c.header)
  return (
    <>
      <div className="toolbar">
        <Search value={q} onChange={setQ} placeholder={`Search ${SOURCE_LABEL[source]}`} style={{ flex: '1 1 260px' }} autoFocus={finePointer()} />
        {available.length > 1 && <Select value={source} onChange={setSource} options={available.map((s) => ({ value: s, label: SOURCE_LABEL[s] }))} style={{ width: 'auto' }} aria-label="Source" />}
        {kinds.length > 1 && <Select value={kind} onChange={setKind} options={kinds} style={{ width: 'auto' }} aria-label="Kind" />}
        {cats.length > 0 && (
          <Select value={cat} onChange={setCat} options={[{ value: '', label: 'Any kind' }, ...cats.map((c) => ({ value: c.id ?? c.name, label: c.name.charAt(0).toUpperCase() + c.name.slice(1).replace(/-/g, ' ') }))]} style={{ width: 'auto' }} aria-label="Category" />
        )}
        <Select value={sort} onChange={setSort} options={SORTS} style={{ width: 'auto' }} aria-label="Sort" />
      </div>
      <div style={{ marginBottom: 6 }}>
        <Check checked={fits} onChange={setFits} label={`Made for ${platformLabel(server.platform)} ${server.mc_version} only`} />
      </div>
      {sources && !plugin && sources.curseforge === false && <p className="small t3" style={{ marginBottom: 8 }}>CurseForge needs an API key in the panel’s settings before it can be searched.</p>}
      <Loaded query={results} sk={<SkItems rows={6} icons />} what="search results">
        {(r) =>
          r.hits.length === 0 ? (
            <Empty title="Nothing matches.">{fits ? 'Untick the box above to see things made for other versions.' : 'Try another search.'}</Empty>
          ) : (
            <>
              <div className="items flush">
                {r.hits.map((h) => {
                  const mine = installedIds.has(h.project_id)
                  return (
                    <div key={h.project_id} className="item" style={{ alignItems: 'flex-start', gap: 14 }}>
                      <button type="button" className="mod-icon-btn" onClick={() => setDetail(h)} aria-label={`About ${h.title}`}>
                        <ModIcon url={h.icon_url} name={h.title} size={44} />
                      </button>
                      <div className="grow">
                        <button type="button" className="title link" style={{ textDecorationColor: 'transparent' }} onClick={() => setDetail(h)}>
                          {h.title}
                        </button>
                        <div className="t2 small">{h.description}</div>
                        <div className="meta">
                          {fmtCompact(h.downloads)} downloads{h.author ? `, by ${h.author}` : ''}
                          {h.client_side === 'required' ? '. Players need it too.' : ''}
                        </div>
                      </div>
                      {mine ? (
                        <span className="t3 small" style={{ paddingTop: 4 }}>
                          Installed
                        </span>
                      ) : (
                        <Btn size="sm" need="mods.install" onClick={() => setInstall(h)}>
                          Install
                        </Btn>
                      )}
                    </div>
                  )
                })}
              </div>
              {r.total_hits > 20 && (
                <div className="row" style={{ justifyContent: 'center', gap: 12, marginTop: 14 }}>
                  <Btn size="sm" disabled={page === 0} onClick={() => setPage((p) => p - 1)}>
                    Previous
                  </Btn>
                  <span className="t3 small num">
                    {page * 20 + 1}–{Math.min(r.total_hits, (page + 1) * 20)} of {fmtNum(r.total_hits)}
                  </span>
                  <Btn size="sm" disabled={(page + 1) * 20 >= r.total_hits} onClick={() => setPage((p) => p + 1)}>
                    Next
                  </Btn>
                </div>
              )}
            </>
          )
        }
      </Loaded>
      {detail && (
        <ProjectSheet
          server={server}
          hit={detail}
          source={source}
          installed={installedIds.has(detail.project_id)}
          onClose={() => setDetail(null)}
          onInstall={() => {
            setInstall(detail)
            setDetail(null)
          }}
        />
      )}
      {install && <InstallDialog server={server} status={status} hit={install} source={source} kind={kind} onClose={() => setInstall(null)} />}
    </>
  )
}

function ProjectSheet({ server, hit, source, installed, onClose, onInstall }: { server: Server; hit: ModSearchHit; source: ModSource; installed: boolean; onClose: () => void; onInstall: () => void }) {
  const project = useQuery({ queryKey: ['mod-project', source, hit.project_id], queryFn: () => api.mods.getProject(server.id, hit.project_id, source), staleTime: 60 * 60_000, retry: false })
  const p = project.data
  return (
    <Sheet
      open
      onClose={onClose}
      title={hit.title}
      sub={`${fmtCompact(hit.downloads)} downloads${p?.updated ? `, updated ${relTime(toMs(p.updated))}` : ''}`}
      foot={
        installed ? (
          <span className="t2">Installed</span>
        ) : (
          <Btn variant="primary" need="mods.install" onClick={onInstall}>
            Install
          </Btn>
        )
      }
    >
      <div className="stack" style={{ gap: 16 }}>
        <div className="row" style={{ gap: 16, alignItems: 'flex-start' }}>
          <ModIcon url={hit.icon_url} name={hit.title} size={72} />
          <p className="grow" style={{ fontSize: 15, paddingTop: 2 }}>
            {p?.description ?? hit.description}
          </p>
        </div>
        <dl className="facts">
          <dt>Kind</dt>
          <dd>{(p?.categories ?? hit.categories).filter((c) => !Object.keys(PLATFORMS).includes(c)).join(', ') || '–'}</dd>
          <dt>Runs on</dt>
          <dd>{hit.client_side === 'required' ? 'The server and every player’s game' : hit.client_side === 'unsupported' ? 'Just the server' : 'The server, and optionally players’ games'}</dd>
          <dt>From</dt>
          <dd>{SOURCE_LABEL[source]}</dd>
          {p?.followers != null && (
            <>
              <dt>Followers</dt>
              <dd className="num">{fmtNum(p.followers)}</dd>
            </>
          )}
        </dl>
        {(p?.source_url || p?.issues_url || p?.wiki_url) && (
          <p className="small row" style={{ gap: 16 }}>
            {p.source_url && (
              <a className="link quiet" href={p.source_url} target="_blank" rel="noopener noreferrer">
                Source
              </a>
            )}
            {p.issues_url && (
              <a className="link quiet" href={p.issues_url} target="_blank" rel="noopener noreferrer">
                Issues
              </a>
            )}
            {p.wiki_url && (
              <a className="link quiet" href={p.wiki_url} target="_blank" rel="noopener noreferrer">
                Wiki
              </a>
            )}
          </p>
        )}
      </div>
    </Sheet>
  )
}

function InstallDialog({ server, status, hit, source, kind, onClose }: { server: Server; status: StatusKey; hit: ModSearchHit; source: ModSource; kind: ModProjectType; onClose: () => void }) {
  const qc = useQueryClient()
  const can = useCan()
  const [sel, setSel] = useState('')
  const [busy, setBusy] = useState(false)
  const loader = kind === 'datapack' ? undefined : server.platform
  const versions = useQuery({
    queryKey: ['mod-versions', source, server.id, hit.project_id, loader ?? '', server.mc_version],
    queryFn: () => api.mods.getVersions(server.id, hit.project_id, loader, server.mc_version, source),
    staleTime: 10 * 60_000,
  })
  useEffect(() => {
    if (versions.data?.length && !sel) setSel(versions.data[0].id)
  }, [versions.data, sel])
  const modpack = kind === 'modpack'
  const install = async () => {
    if (!sel) return
    setBusy(true)
    const r = await act(`install ${hit.title}`, () => (modpack ? api.mods.installModpack(server.id, hit.project_id, sel) : api.mods.install(server.id, source, hit.project_id, sel, true)).then(() => true))
    setBusy(false)
    if (r === undefined) return
    toast('ok', `Installed ${hit.title}`, status === 'online' ? 'Restart the server to load it.' : 'It loads the next time the server starts.')
    void qc.invalidateQueries({ queryKey: ['mods', server.id] })
    onClose()
  }
  const options = useMemo(
    () =>
      (versions.data ?? []).slice(0, 25).map((v, i) => ({
        value: v.id,
        label: `${v.version_number}${v.version_type && v.version_type !== 'release' ? ` (${v.version_type})` : ''}${i === 0 ? ', newest' : ''}`,
        hint: fmtDate(toMs(v.date_published) ?? 0, false),
      })),
    [versions.data],
  )
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Install ${hit.title}`}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" disabled={!sel || !can('mods.install')} loading={busy} onClick={() => void install()}>
            Install
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 14 }}>
        <div className="row" style={{ gap: 12 }}>
          <ModIcon url={hit.icon_url} name={hit.title} size={40} />
          <p className="t2 small grow">{hit.description}</p>
        </div>
        <Loaded query={versions} sk={<SkItems rows={1} />} what="its versions">
          {(list) =>
            list.length === 0 ? (
              <Notice tone="warn">
                There’s no version of {hit.title} for {platformLabel(server.platform)} {server.mc_version}.
              </Notice>
            ) : (
              <Field label="Version">
                <Select value={sel} onChange={setSel} options={options} aria-label="Version" />
              </Field>
            )
          }
        </Loaded>
        {modpack ? (
          <p className="t2 small">A modpack replaces this server’s mods with its own. Back up first if you might want them back.</p>
        ) : (
          <p className="t2 small">Anything it needs is installed with it.</p>
        )}
        {hit.client_side === 'required' && <p className="t2 small">Players will need {hit.title} on their side as well.</p>}
      </div>
    </Dialog>
  )
}
