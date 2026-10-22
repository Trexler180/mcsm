import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { InstalledMod, ModImpact, ModProjectType, ModSource, ModUpdate, ModUpdateRun, ModUpdateStep, ModVersion, MyServerPermissions, Server, SkippedModVersion } from '@/lib/types'
import { Btn, Check, Choices, Empty, Filters, IconBtn, Search, Switch, Tabs, TextBtn, type StatusKey } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtDate } from '@/ui/format'
import { Dialog, MenuButton, MenuItem, MenuSep } from '@/ui/overlays'
import { PLATFORMS, contentLabel } from '@/ui/platforms'
import { Loaded, SheetSkeleton, SkItems, SkTable } from '@/ui/skeleton'
import { toast } from '@/ui/toasts'
import { cx, plural, toMs } from '@/ui/util'
import { allowed } from './data'
import { ConfigsView } from './configs'
import { Browse, Markdown, ProjectSheet } from './mod-browse'
import { gameVersions } from './mod-shared'
import { InstalledModIcon } from './mod-icon'

// Mods, plugins or datapacks: what's installed and how to change it, finding
// more, and their config files. Changes load the next time the server starts.


export type ModsView = 'installed' | 'browse' | 'configs'

export function modsViewOf(sub: string | undefined): ModsView {
  return sub === 'browse' || sub === 'configs' ? sub : 'installed'
}

export function ModsTab({ server, status, perms, view, setView }: { server: Server; status: StatusKey; perms: MyServerPermissions | undefined; view: ModsView; setView: (v: ModsView) => void }) {
  const qc = useQueryClient()
  const mods = useQuery({ queryKey: ['mods', server.id], queryFn: () => api.mods.list(server.id) })
  const updatesQ = useQuery({ queryKey: ['mod-updates', server.id], queryFn: () => api.mods.updates(server.id), staleTime: 5 * 60_000, retry: false })
  const updates = updatesQ.data ?? []
  const [safe, setSafe] = useState(false)
  const [pct, setPct] = useState<number | null>(null)
  // Browse stays mounted once opened, so its search and filters survive a
  // look at what's installed.
  const [browsed, setBrowsed] = useState(view === 'browse')
  useEffect(() => {
    if (view === 'browse') setBrowsed(true)
  }, [view])
  const [found, setFound] = useState<number | null>(null)
  const upRef = useRef<HTMLInputElement>(null)
  const word = contentLabel(server.platform)
  const installedById = new Map((mods.data ?? []).map((m) => [m.id, m]))
  // The safe update leaves turned-off and pinned mods alone. Names come from the
  // installed list, which the rescan keeps as project titles; the update list
  // can be older than that and still carry a version's name.
  const pending = updates
    .filter((u) => u.enabled && !installedById.get(u.mod_id)?.pinned)
    .map((u) => ({ ...u, name: installedById.get(u.mod_id)?.name ?? u.name }))
  const offWithUpdates = updates.filter((u) => !u.enabled && !installedById.get(u.mod_id)?.pinned).length
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
            { value: 'browse', label: 'Browse', count: found ?? undefined },
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
          <Btn
            variant="primary"
            need="mods.update"
            tip={offWithUpdates ? `${plural(offWithUpdates, 'turned-off ' + word.toLowerCase().replace(/s$/, ''))} with an update ${offWithUpdates === 1 ? 'is' : 'are'} left as ${offWithUpdates === 1 ? 'it is' : 'they are'}` : undefined}
            onClick={() => setSafe(true)}
          >
            Update {pending.length}
          </Btn>
        )}
      </div>
      {view === 'installed' && (
        <Loaded query={mods} sk={<SkTable rows={8} icons />} what={`${word.toLowerCase()} on ${server.name}`}>
          {(list) => <Installed server={server} status={status} list={list} updates={updates} updatesPending={updatesQ.isPending} />}
        </Loaded>
      )}
      {browsed && (
        <div hidden={view !== 'browse'}>
          <Browse server={server} status={status} installed={mods.data ?? []} onTotal={setFound} />
        </div>
      )}
      {view === 'configs' && <ConfigsView server={server} />}
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

const sourceName = (s: string) => ({ modrinth: 'From Modrinth', curseforge: 'From CurseForge', hangar: 'From Hangar', spigotmc: 'From SpigotMC' })[s] ?? ''

function projectUrl(m: InstalledMod): string | null {
  if (!m.source_id) return null
  if (m.source === 'modrinth') return `https://modrinth.com/project/${m.source_id}`
  if (m.source === 'hangar') return `https://hangar.papermc.io/${m.source_id}`
  if (m.source === 'spigotmc') return `https://www.spigotmc.org/resources/${m.source_id}`
  return null
}

type Show = 'all' | 'updates' | 'on' | 'off'

/** What an installed file is, for looking it up again at its source. */
function kindOf(m: InstalledMod, platform: string): ModProjectType {
  if (m.install_path.includes('datapacks')) return 'datapack'
  return PLATFORMS[platform]?.content === 'plugin' ? 'plugin' : 'mod'
}

function Installed({ server, status, list, updates, updatesPending }: { server: Server; status: StatusKey; list: InstalledMod[]; updates: ModUpdate[]; updatesPending: boolean }) {
  const qc = useQueryClient()
  const [q, setQ] = useState('')
  const [show, setShow] = useState<Show>('all')
  const [impact, setImpact] = useState<{ mod: InstalledMod; mode: 'uninstall' | 'disable' } | null>(null)
  const [versions, setVersions] = useState<InstalledMod | null>(null)
  const [about, setAbout] = useState<InstalledMod | null>(null)
  const [checking, setChecking] = useState(false)
  const skipped = useQuery({ queryKey: ['mod-skipped', server.id], queryFn: () => api.mods.skippedVersions(server.id), staleTime: 60_000, retry: false }).data ?? []
  const word = contentLabel(server.platform).toLowerCase()
  const updateOf = (m: InstalledMod) => updates.find((u) => u.mod_id === m.id)
  const skippedOf = (m: InstalledMod) => skipped.filter((v) => v.project_id === m.source_id)
  const needle = q.trim().toLowerCase()
  const hasUpdate = (m: InstalledMod) => !!updateOf(m) && !m.pinned
  const on = list.filter((m) => m.enabled).length
  const counts: Record<Show, number> = { all: list.length, updates: list.filter(hasUpdate).length, on, off: list.length - on }
  const rows = list
    .filter((m) => show === 'all' || (show === 'updates' ? hasUpdate(m) : show === 'on' ? m.enabled : !m.enabled))
    .filter((m) => !needle || m.name.toLowerCase().includes(needle) || m.file_name.toLowerCase().includes(needle))
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
      <UpdateRuns server={server} />
      <div className="toolbar">
        <Filters
          value={show}
          onChange={setShow}
          label={`Which ${word}`}
          options={[
            { value: 'all', label: 'All', count: counts.all },
            // Until the first check answers there is no count to give: "0" would
            // read as "everything is current".
            { value: 'updates', label: 'Updates', count: updatesPending ? undefined : counts.updates },
            { value: 'on', label: 'On', count: counts.on },
            { value: 'off', label: 'Off', count: counts.off },
          ]}
        />
        <Search value={q} onChange={setQ} placeholder={`Filter ${word}`} size="sm" style={{ width: 220 }} />
        <span className="grow" />
        <TextBtn quiet onClick={() => void check()}>
          {checking || updatesPending ? 'Checking…' : 'Check for updates'}
        </TextBtn>
      </div>
      {rows.length === 0 ? (
        <Empty title={!list.length ? `No ${word} yet.` : show === 'updates' && !needle ? (updatesPending ? 'Checking for updates…' : 'Everything is up to date.') : 'Nothing matches.'}>{!list.length && 'Find some under Browse, or upload a jar.'}</Empty>
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
                        {sourceName(m.source) && <div className="sub t3">{sourceName(m.source)}</div>}
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
                          <MenuItem close={close} disabled={!m.source_id} onClick={() => setAbout(m)}>
                            About it
                          </MenuItem>
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
      {about?.source_id && <ProjectSheet server={server} source={about.source as ModSource} kind={kindOf(about, server.platform)} projectId={about.source_id} installed onClose={() => setAbout(null)} />}
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
  const picked = versions.data?.find((v) => v.id === sel)
  // Search lists leave changelogs out; the version itself has it.
  const notes = useQuery({
    queryKey: ['mod-version', mod.source, mod.source_id, sel],
    queryFn: () => api.mods.getVersion(server.id, mod.source_id as string, sel, mod.source),
    enabled: !!picked && !picked.changelog,
    staleTime: 60 * 60_000,
    retry: false,
  })
  const changelog = picked?.changelog || notes.data?.changelog
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
            <div style={{ maxHeight: changelog ? 260 : 420, overflowY: 'auto' }}>
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
      {picked && (changelog || notes.isLoading) && (
        <div className="changelog">
          <div className="field-label t2">What’s new in {picked.version_number}</div>
          {changelog ? <Markdown text={changelog} /> : <p className="t3 small">Loading…</p>}
        </div>
      )}
    </Dialog>
  )
}

export { gameVersions }

function SafeUpdateDialog({ server, status, updates, onClose }: { server: Server; status: StatusKey; updates: ModUpdate[]; onClose: () => void }) {
  const qc = useQueryClient()
  const [backup, setBackup] = useState(true)
  const [how, setHow] = useState<'safe' | 'swap'>('safe')
  const up = status === 'online'
  // Just the files, one after another, with nothing restarted or watched.
  const swap = async () => {
    onClose()
    let done = 0
    const failed: string[] = []
    for (const u of updates) {
      try {
        await api.mods.update(server.id, u.mod_id, u.latest_version_id)
        done++
      } catch {
        failed.push(u.name)
      }
    }
    void qc.invalidateQueries({ queryKey: ['mods', server.id] })
    void qc.invalidateQueries({ queryKey: ['mod-updates', server.id] })
    if (done) toast('ok', `Updated ${plural(done, 'mod')} on ${server.name}`, up ? 'Restart the server to load them.' : 'They load the next time it starts.')
    if (failed.length) toast('bad', `Couldn’t update ${failed.join(', ')}`, 'Try those one at a time to see why.')
  }
  const start = () => {
    if (how === 'swap') {
      void swap()
      return
    }
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
            {how === 'safe' ? 'Update safely' : 'Update the files'}
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
        <Choices
          value={how}
          onChange={setHow}
          options={[
            {
              value: 'safe',
              label: 'Update safely',
              desc: `MCSM swaps the files, ${up ? 'restarts the server' : 'starts the server once'} and watches it come up. If an update stops it from starting, that update is undone and skipped next time.${up ? ' Anyone online is disconnected for a minute.' : ''}`,
            },
            { value: 'swap', label: 'Just update the files', desc: up ? 'Nothing restarts; they load the next time you restart it. Nothing checks that it still starts.' : 'They load the next time it starts. Nothing checks that it still does.' },
          ]}
        />
        {how === 'safe' && <Check checked={backup} onChange={setBackup} label="Back up first" />}
      </div>
    </Dialog>
  )
}

const PHASE: Record<string, string> = {
  checking: 'Checking for updates',
  applying: 'Swapping the files',
  verifying: 'Starting the server and watching it come up',
  isolating: 'It didn’t start. Finding the update that broke it',
  reverting: 'It didn’t start. Undoing the updates',
  restoring: 'Putting the server back how it was',
  done: 'Finishing',
}

const OUTCOME: Record<string, { text: string; tone: '' | 'ok' | 'warn' | 'bad' }> = {
  success: { text: 'The safe update finished.', tone: 'ok' },
  no_updates: { text: 'The safe update found nothing to update.', tone: '' },
  partial: { text: 'The safe update finished, but not everything updated.', tone: 'warn' },
  reverted: { text: 'An update stopped the server starting, so it was undone and will be skipped.', tone: 'warn' },
  failed: { text: 'The safe update failed.', tone: 'bad' },
}

export function stepText(s: ModUpdateStep): string {
  switch (s.status) {
    case 'updated':
      return `${s.from_version} to ${s.to_version}`
    case 'reverted_skipped':
      return `${s.to_version} stopped the server starting, so it’s back on ${s.from_version}`
    case 'failed':
      return s.error || 'Couldn’t update'
    default:
      return `${s.from_version} to ${s.to_version}…`
  }
}

/** A safe update while it runs, and how it went for ten minutes after. */
function UpdateRuns({ server }: { server: Server }) {
  const qc = useQueryClient()
  const [dismissed, setDismissed] = useState<string | null>(null)
  const runs = useQuery({
    queryKey: ['mod-update-runs', server.id],
    queryFn: () => api.mods.updateRuns(server.id, 5),
    refetchInterval: (q) => (q.state.data?.some((r) => r.status === 'running') ? 2500 : false),
    retry: false,
  }).data
  const active = runs?.find((r) => r.status === 'running') ?? null
  const was = useRef<string | null>(null)
  useEffect(() => {
    // When the run being watched ends, what's installed and what's offered moved.
    if (was.current && !active) {
      for (const key of ['mods', 'mod-updates', 'mod-skipped']) void qc.invalidateQueries({ queryKey: [key, server.id] })
    }
    was.current = active?.id ?? null
  }, [active, qc, server.id])
  const last = runs?.[0]
  const recent = !active && last && last.status !== 'running' && last.id !== dismissed && (toMs(last.finished_at) ?? 0) > Date.now() - 10 * 60_000 ? last : null
  const run: ModUpdateRun | null = active ?? recent
  if (!run) return null
  const steps = run.detail?.mods ?? []
  const outcome = OUTCOME[run.status]
  return (
    <div className={cx('notice run-notice', active ? '' : outcome?.tone)} style={{ marginBottom: 14 }} aria-live="polite">
      <div className="row" style={{ alignItems: 'flex-start', gap: 10 }}>
        <div className="grow">
          <p className="strong">{active ? `${PHASE[run.detail?.phase ?? 'checking'] ?? 'Working'}…` : (outcome?.text ?? 'The safe update finished.')}</p>
          {run.detail?.message && <p className="small t2">{run.detail.message}</p>}
          {steps.length > 0 && (
            <ul className="run-steps small">
              {steps.map((st) => (
                <li key={st.mod_id} className={cx(st.status === 'failed' && 'bad', st.status === 'reverted_skipped' && 'warn')}>
                  <span className="strong">{st.name}</span> <span className="t2">{stepText(st)}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
        {!active && <IconBtn icon="x" label="Dismiss" onClick={() => setDismissed(run.id)} />}
      </div>
    </div>
  )
}
