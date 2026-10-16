import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { ModCompat, MyServerPermissions, Server, VersionMigration } from '@/lib/types'
import { useAuthStore } from '@/store/auth'
import { Btn, Check, Field, Meter, Select, TextBtn, type StatusKey } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtDate } from '@/ui/format'
import { PLATFORMS, contentLabel, platformLabel } from '@/ui/platforms'
import { SaveBar } from '@/ui/savebar'
import { ask } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { cx, plural, toMs } from '@/ui/util'
import { allowed } from './data'

// Version & Java: moving to another Minecraft version (with every mod checked
// first and a backup to fall back on), switching or repairing the server
// software, and the Java it runs on.

/** The Java a Minecraft version needs at least. */
export function javaFor(mc: string): number {
  const m = mc.match(/^1\.(\d+)(?:\.(\d+))?/)
  if (!m) return 21
  const minor = +m[1]
  const patch = +(m[2] ?? 0)
  if (minor > 20 || (minor === 20 && patch >= 5)) return 21
  if (minor >= 18) return 17
  if (minor === 17) return 16
  return 8
}

/** Compares Minecraft versions; snapshots and odd names sort as equal. */
function compareMc(a: string, b: string): number {
  const pa = a.split(/[.-]/).map((x) => parseInt(x, 10))
  const pb = b.split(/[.-]/).map((x) => parseInt(x, 10))
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const x = pa[i] || 0
    const y = pb[i] || 0
    if (Number.isNaN(x) || Number.isNaN(y)) return 0
    if (x !== y) return x - y
  }
  return 0
}

// Aikar's flags, the usual advice for a Minecraft server's garbage collector.
const USUAL_FLAGS = [
  '-XX:+UseG1GC',
  '-XX:+ParallelRefProcEnabled',
  '-XX:MaxGCPauseMillis=200',
  '-XX:+UnlockExperimentalVMOptions',
  '-XX:+DisableExplicitGC',
  '-XX:+AlwaysPreTouch',
  '-XX:G1NewSizePercent=30',
  '-XX:G1MaxNewSizePercent=40',
  '-XX:G1HeapRegionSize=8M',
  '-XX:G1ReservePercent=20',
  '-XX:G1HeapWastePercent=5',
  '-XX:G1MixedGCCountTarget=4',
  '-XX:InitiatingHeapOccupancyPercent=15',
  '-XX:G1MixedGCLiveThresholdPercent=90',
  '-XX:G1RSetUpdatingPauseIntervalPercent=5',
  '-XX:SurvivorRatio=32',
  '-XX:+PerfDisableSharedMem',
  '-XX:MaxTenuringThreshold=1',
]

export function VersionSettings({ server, status, perms }: { server: Server; status: StatusKey; perms: MyServerPermissions | undefined }) {
  return (
    <div>
      <VersionChange server={server} status={status} perms={perms} />
      <SoftwareSection server={server} status={status} />
      {server.java_binary !== null && <RuntimeSection server={server} status={status} />}
    </div>
  )
}

// ---- moving to another version ------------------------------------------------------------

const STATUS_ORDER = ['incompatible', 'unknown', 'unmanaged', 'supported', 'compatible']
const COMPAT_TEXT: Record<string, string> = {
  compatible: 'Works as it is',
  supported: 'Updates',
  incompatible: 'Turned off: nothing for this version yet',
  unmanaged: 'Can’t check: you uploaded it',
  unknown: 'Couldn’t check',
}
const PHASES: Record<string, string> = {
  checking: 'checking the mods',
  backup: 'backing up',
  applying: 'changing the version and mods',
  verifying: 'starting it once to check',
  restoring: 'it didn’t start; putting the backup back',
  done: 'done',
}

function VersionChange({ server, status, perms }: { server: Server; status: StatusKey; perms: MyServerPermissions | undefined }) {
  const qc = useQueryClient()
  const [target, setTarget] = useState('')
  const [checked, setChecked] = useState('')
  const [snapshots, setSnapshots] = useState(false)
  const [runId, setRunId] = useState<string | null>(null)
  const versions = useQuery({ queryKey: ['mc-versions', server.platform, snapshots], queryFn: () => api.minecraft.versions(server.platform, snapshots), staleTime: 30 * 60_000 }).data ?? []
  const history = useQuery({ queryKey: ['migrations', server.id], queryFn: () => api.servers.migrations(server.id, 5), staleTime: 60_000, retry: false }).data ?? []
  const check = useQuery({ queryKey: ['version-check', server.id, checked], queryFn: () => api.mods.versionCheck(server.id, checked), enabled: !!checked, staleTime: 60_000, retry: false })
  const inFlight = history.find((m) => m.status === 'running')
  const run = useQuery({
    queryKey: ['migration', server.id, runId ?? inFlight?.id],
    queryFn: () => api.servers.migration(server.id, (runId ?? inFlight?.id) as string),
    enabled: !!(runId ?? inFlight?.id),
    refetchInterval: (q) => (q.state.data?.status === 'running' || !q.state.data ? 2000 : false),
  }).data
  const running = run?.status === 'running'
  useEffect(() => {
    if (!run || run.status === 'running' || !runId) return
    setRunId(null)
    setTarget('')
    setChecked('')
    for (const k of [['server', server.id], ['mods', server.id], ['mod-updates', server.id], ['backups', server.id], ['migrations', server.id], ['fleet']]) void qc.invalidateQueries({ queryKey: k })
    const done = run.status === 'success' ? 'ok' : run.status === 'partial' ? 'warn' : 'bad'
    toast(done, migrationSentence(run), run.status === 'reverted' ? 'The backup was put back, so nothing changed.' : undefined)
  }, [run, runId, qc, server.id])

  const older = !!target && compareMc(target, server.mc_version) < 0
  const options = versions.filter((v) => v.version !== server.mc_version).map((v) => ({ value: v.version, label: v.version + (compareMc(v.version, server.mc_version) < 0 ? ' (older)' : '') + (v.stable ? '' : ' (snapshot)') }))
  const counts = check.data?.counts ?? {}
  const mods = useMemo(() => [...(check.data?.mods ?? [])].filter((m) => m.enabled).sort((a, b) => STATUS_ORDER.indexOf(a.status) - STATUS_ORDER.indexOf(b.status)), [check.data])
  const word = contentLabel(server.platform).toLowerCase()
  const canMove = allowed(perms, 'settings') && allowed(perms, 'mods.update') && allowed(perms, 'backups.create')

  const move = async () => {
    const off = counts.incompatible ?? 0
    const ok = await ask({
      title: `Move ${server.name} to ${checked}?`,
      body: `${off ? plural(off, word.replace(/s$/, '')) + ' will be turned off. ' : ''}A backup is made first, and put back automatically if it doesn’t start.${status === 'online' || status === 'starting' ? ' The server restarts.' : ''}`,
      confirm: `Move to ${checked}`,
      danger: older,
    })
    if (!ok) return
    const r = await act(`move ${server.name} to ${checked}`, () => api.servers.migrate(server.id, checked))
    if (r === undefined) return
    setRunId(r.id)
    void qc.invalidateQueries({ queryKey: ['migrations', server.id] })
  }

  return (
    <div className="form-section">
      <h2>Minecraft version</h2>
      <p className="lede">
        {server.name} runs {platformLabel(server.platform)} for Minecraft {server.mc_version}.{' '}
        {PLATFORMS[server.platform]?.content === 'datapack' ? 'Before moving to another version, MCSM makes a backup to fall back on.' : `Before moving to another version, MCSM checks every ${word.replace(/s$/, '')} for a matching build.`}
      </p>
      {running && run ? (
        <div className="progress-banner" style={{ margin: 0 }} role="status">
          <span className="grow">
            Moving to Minecraft {run.to_mc_version}: {PHASES[run.detail?.phase ?? 'checking'] ?? run.detail?.phase}.
          </span>
          <span className="track">
            <Meter busy value={0} max={1} label="Progress" />
          </span>
        </div>
      ) : (
        <>
          <div className="row wrap" style={{ gap: 8 }}>
            <Select value={target} onChange={(v) => setTarget(v)} options={options} placeholder="Choose a version" style={{ width: 240 }} aria-label="Target version" />
            <Btn disabled={!target || target === checked} loading={check.isFetching} onClick={() => setChecked(target)}>
              Check
            </Btn>
            <Check checked={snapshots} onChange={setSnapshots} label="Include snapshots" />
          </div>
          {older && (
            <p className="warn small" style={{ marginTop: 10 }}>
              Going back is risky. A world opened in a newer version can lose blocks and items in an older one.
            </p>
          )}
          {checked && check.data && (
            <div style={{ marginTop: 18 }}>
              <p style={{ marginBottom: 10 }}>
                {mods.length === 0 ? (
                  `There are no ${word} to check.`
                ) : (
                  <>
                    {counts.supported ?? 0} will be updated and {counts.compatible ?? 0} work as they are.
                    {counts.incompatible ? (
                      <>
                        {' '}
                        <span className="bad">{counts.incompatible} would be turned off</span> until there’s a build for {checked}.
                      </>
                    ) : null}
                    {(counts.unmanaged ?? 0) + (counts.unknown ?? 0) ? ` ${(counts.unmanaged ?? 0) + (counts.unknown ?? 0)} can’t be checked.` : ''}
                  </>
                )}
              </p>
              {mods.length > 0 && <CompatTable mods={mods} />}
              <div style={{ marginTop: 14 }}>
                <Btn variant={older ? 'danger primary' : 'primary'} disabled={!canMove} tip={canMove ? undefined : 'Needs Settings, Mods: update and Backups: create'} onClick={() => void move()}>
                  Move to {checked}
                </Btn>
              </div>
            </div>
          )}
        </>
      )}
      {history.filter((m) => m.status !== 'running').length > 0 && (
        <p className="small t3" style={{ marginTop: 16 }}>
          {history
            .filter((m) => m.status !== 'running')
            .slice(0, 3)
            .map((m) => `${migrationSentence(m)} on ${fmtDate(toMs(m.started_at) ?? 0, false)}.`)
            .join(' ')}
        </p>
      )}
    </div>
  )
}

function migrationSentence(m: VersionMigration): string {
  const changed = m.detail?.mods ?? []
  const updated = changed.filter((x) => x.action === 'update' && x.status === 'done').length
  const off = changed.filter((x) => x.action === 'disable').length
  switch (m.status) {
    case 'success':
      return `Moved from ${m.from_mc_version} to ${m.to_mc_version}${updated || off ? `: ${updated} updated, ${off} turned off` : ''}`
    case 'partial':
      return `Moved from ${m.from_mc_version} to ${m.to_mc_version}, with some mods turned off`
    case 'reverted':
      return `Tried moving to ${m.to_mc_version}; it didn’t start, so the backup was put back`
    case 'failed':
      return `Couldn’t move to ${m.to_mc_version}${m.detail?.message ? `: ${m.detail.message}` : ''}`
    default:
      return `Moving to ${m.to_mc_version}`
  }
}

function CompatTable({ mods }: { mods: ModCompat[] }) {
  return (
    <table className="t">
      <tbody>
        {mods.map((m) => (
          <tr key={m.mod_id}>
            <td>
              {m.name}
              {m.pinned && <span className="t3 small"> pinned</span>}
              {m.dep_warnings && m.dep_warnings.length > 0 && <div className="sub warn">Needs {m.dep_warnings.join(', ')}, which would be turned off.</div>}
            </td>
            <td className={cx('small', m.status === 'incompatible' ? 'bad' : 't2')}>{m.status === 'supported' && m.target_version ? `Updates to ${m.target_version}` : COMPAT_TEXT[m.status] ?? m.status}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

// ---- switching or repairing the software ------------------------------------------------------

function SoftwareSection({ server, status }: { server: Server; status: StatusKey }) {
  const qc = useQueryClient()
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  const [platform, setPlatform] = useState(server.platform)
  const [mc, setMc] = useState(server.mc_version)
  const [loader, setLoader] = useState(server.loader_version ?? '')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    setPlatform(server.platform)
    setMc(server.mc_version)
    setLoader(server.loader_version ?? '')
  }, [server])
  const hasLoader = !!PLATFORMS[platform] && ['fabric', 'quilt', 'forge', 'neoforge'].includes(platform)
  const versions = useQuery({ queryKey: ['mc-versions', platform, false], queryFn: () => api.minecraft.versions(platform, false), staleTime: 30 * 60_000 }).data ?? []
  const loaders = useQuery({ queryKey: ['mc-loaders', platform], queryFn: () => api.minecraft.loaders(platform), staleTime: 30 * 60_000, enabled: hasLoader }).data ?? []
  const pin = hasLoader ? loader : ''
  const changed = platform !== server.platform || mc !== server.mc_version || pin !== (server.loader_version ?? '')
  const live = status === 'online' || status === 'starting' || status === 'stopping'
  const imported = !!server.settings?.import
  const apply = async () => {
    const what = `${platformLabel(platform)} ${mc}${pin ? `, loader ${pin}` : ''}`
    const ok = await ask({
      title: changed ? `Switch ${server.name} to ${what}?` : `Reinstall ${what}?`,
      body: [
        changed ? 'The server software is downloaded and installed. Mods aren’t moved; to change the Minecraft version with its mods, use the section above.' : 'The server software is downloaded and installed again, which fixes a broken or missing jar.',
        live && 'The server is stopped first.',
        imported && 'This server was imported, so it switches to a jar the panel manages.',
      ]
        .filter(Boolean)
        .join(' '),
      confirm: changed ? 'Switch' : 'Reinstall',
      danger: platform !== server.platform,
    })
    if (!ok) return
    setBusy(true)
    const r = await act(changed ? `switch to ${what}` : 'reinstall the server software', async () => {
      const accepted = await api.servers.reinstall(server.id, { platform, mc_version: mc, loader_version: pin || null })
      await api.operations.wait(server.id, accepted?.operation)
      return true
    })
    setBusy(false)
    if (r === undefined) return
    toast('ok', changed ? `${server.name} runs ${what} now` : 'Reinstalled', 'Start it when you’re ready.')
    void qc.invalidateQueries({ queryKey: ['server', server.id] })
    void qc.invalidateQueries({ queryKey: ['fleet'] })
  }
  return (
    <div className="form-section">
      <h2>Server software</h2>
      <p className="lede">Switch to other software, pin a loader build, or reinstall the jar if it’s broken.</p>
      <div className="form-grid">
        <Field label="Software">
          <Select value={platform} onChange={(v) => setPlatform(v)} disabled={!admin} options={Object.entries(PLATFORMS).map(([value, p]) => ({ value, label: p.label }))} aria-label="Software" />
        </Field>
        <Field label="Minecraft">
          <Select value={mc} onChange={setMc} options={versions.length ? versions.map((v) => v.version) : [mc]} aria-label="Minecraft version" />
        </Field>
        {hasLoader && (
          <Field label={`${platformLabel(platform)} loader`} hint="Leave on newest to follow the latest build.">
            <Select value={loader} onChange={setLoader} options={[{ value: '', label: 'Newest' }, ...loaders.map((v, i) => ({ value: v.version, label: v.version + (i === 0 ? ', newest' : '') }))]} aria-label="Loader" />
          </Field>
        )}
      </div>
      <div className="row" style={{ gap: 8, marginTop: 12 }}>
        <Btn need="settings" loading={busy} onClick={() => void apply()}>
          {changed ? 'Switch' : 'Reinstall'}
        </Btn>
        {changed && (
          <TextBtn
            quiet
            onClick={() => {
              setPlatform(server.platform)
              setMc(server.mc_version)
              setLoader(server.loader_version ?? '')
            }}
          >
            Undo
          </TextBtn>
        )}
      </div>
    </div>
  )
}

// ---- the Java it runs on ----------------------------------------------------------------------

function RuntimeSection({ server, status }: { server: Server; status: StatusKey }) {
  const qc = useQueryClient()
  const javas = useQuery({ queryKey: ['java', server.id], queryFn: () => api.servers.javaInstallations(server.id), staleTime: 5 * 60_000, retry: false }).data
  const source = useMemo(() => ({ java: server.java_binary ?? 'java', flags: (server.jvm_args ?? []).join('\n') }), [server])
  const [d, setD] = useState(source)
  useEffect(() => setD(source), [source])
  const needs = javaFor(server.mc_version)
  const list = javas?.installations ?? []
  const chosen = list.find((j) => j.path === d.java)
  const custom = !chosen && d.java !== 'java'
  const flags = d.flags.split('\n').map((x) => x.trim()).filter(Boolean)
  const usual = USUAL_FLAGS.every((f) => flags.includes(f))
  const dirty = d.java !== source.java || d.flags.trim() !== source.flags.trim()
  const live = status === 'online' || status === 'starting'
  const save = async () => {
    await api.servers.update(server.id, { java_binary: d.java.trim() || 'java', jvm_args: flags })
    toast('ok', 'Saved', live ? 'It takes effect after a restart.' : undefined, {
      actions: live ? [{ label: 'Restart now', run: () => void act(`restart ${server.name}`, () => api.servers.restart(server.id)) }] : undefined,
    })
    void qc.invalidateQueries({ queryKey: ['server', server.id] })
  }
  return (
    <div className="form-section" style={{ borderBottom: 0 }}>
      <h2>How it starts</h2>
      <p className="lede">Only administrators see this: the Java the node runs it with, and the flags it passes.</p>
      <div className="form-grid">
        <Field label="Java" error={chosen && chosen.major < needs ? `Minecraft ${server.mc_version} needs Java ${needs} or newer, so it won’t start.` : null}>
          <Select
            value={chosen ? chosen.path : custom ? '__custom' : 'java'}
            onChange={(v) => setD((p) => ({ ...p, java: v === '__custom' ? (custom ? p.java : '') : v }))}
            options={[
              { value: 'java', label: 'The node’s default java' },
              ...list.map((j) => ({ value: j.path, label: `Java ${j.major}${j.major === needs ? ', recommended' : ''}`, hint: j.version })),
              { value: '__custom', label: 'Another path…' },
            ]}
            aria-label="Java"
          />
        </Field>
        {(custom || (!chosen && d.java === '')) && (
          <Field label="Path to java">
            <input className="input mono" value={d.java} onChange={(e) => setD((p) => ({ ...p, java: e.target.value }))} placeholder="/usr/lib/jvm/java-21/bin/java" />
          </Field>
        )}
        <Field
          label="Java flags"
          className="span"
          aside={
            <TextBtn
              quiet
              onClick={() =>
                setD((p) => ({
                  ...p,
                  flags: usual ? flags.filter((x) => !USUAL_FLAGS.includes(x)).join('\n') : Array.from(new Set([...flags, ...USUAL_FLAGS])).join('\n'),
                }))
              }
            >
              {usual ? 'Remove the usual flags' : 'Add the usual flags'}
            </TextBtn>
          }
          hint="One per line. Memory comes from General and is added for you."
        >
          <textarea className="textarea mono" rows={6} value={d.flags} onChange={(e) => setD((p) => ({ ...p, flags: e.target.value }))} style={{ fontSize: 12 }} />
        </Field>
      </div>
      <SaveBar dirty={dirty} onSave={save} onReset={() => setD(source)} />
    </div>
  )
}
