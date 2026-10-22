import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { parseNbt } from '@/lib/nbt'
import type { FileTree, Server } from '@/lib/types'
import { defaultServerPropertiesFor, parseProperties, serializeProperties } from '@/pages/server/properties-schema'
import { useUploadStats } from '@/lib/upload-stats'
import { Btn, Check, Copy, Empty, Field, Meter, Search, Switch, type StatusKey } from '@/ui/controls'
import { Spark } from '@/ui/charts'
import { act, failureOf } from '@/ui/failure'
import { fmtBytes, fmtDur, relTime } from '@/ui/format'
import { Dialog, MenuButton, MenuItem, MenuSep } from '@/ui/overlays'
import { useCan } from '@/ui/perm'
import { Loaded, Sk, SkTable } from '@/ui/skeleton'
import { ask } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { cap, cx } from '@/ui/util'
import { isLiveStatus } from './data'
import { DIMENSIONS, linkedFolders, parseWorldInfo, type WorldInfo } from './world-info'

// The worlds in a server's folder (any folder holding a level.dat), which one
// loads, and what the loaded one is like. One world loads at a time; switching
// takes effect the next time the server starts.

export interface WorldRow {
  folder: string
  /** Its Nether and End, when the server keeps them in folders beside it. */
  linked: string[]
  /** Bytes, with its linked folders; undefined when the node can't say. */
  size?: number
  /** When level.dat was last written, as epoch ms. */
  saved?: number
}

/** Worlds from a depth-2 tree of the server root, the loaded one first. */
export function findWorlds(tree: FileTree | undefined, loaded: string): WorldRow[] {
  if (!tree) return []
  const saved = new Map<string, number>()
  for (const e of tree.entries) {
    const m = /^([^/]+)\/level\.dat$/.exec(e.path)
    if (m) saved.set(m[1], Date.parse(e.modified) || 0)
  }
  const linkedTo = new Map<string, string>()
  for (const folder of saved.keys()) {
    for (const l of linkedFolders(folder)) if (saved.has(l)) linkedTo.set(l, folder)
  }
  const rows: WorldRow[] = []
  for (const [folder, at] of saved) {
    if (linkedTo.has(folder)) continue
    const linked = linkedFolders(folder).filter((l) => linkedTo.get(l) === folder)
    const size = tree.sizes ? [folder, ...linked].reduce((n, f) => n + (tree.sizes?.[f] ?? 0), 0) : undefined
    rows.push({ folder, linked, size, saved: at || undefined })
  }
  return rows.sort((a, b) => Number(b.folder === loaded) - Number(a.folder === loaded) || a.folder.localeCompare(b.folder))
}

function useWorld(serverId: string, w: WorldRow) {
  return useQuery({
    queryKey: ['world', serverId, w.folder, w.linked.join(',')],
    queryFn: async () => {
      const [bytes, ...lists] = await Promise.all([
        api.files.viewBytes(serverId, `/${w.folder}/level.dat`),
        ...[w.folder, ...w.linked].map((f) => api.files.list(serverId, `/${f}`).catch(() => null)),
      ])
      const { root } = await parseNbt(bytes)
      const found = new Set(lists.flatMap((l) => l?.entries.filter((e) => e.type === 'dir' && DIMENSIONS[e.name]).map((e) => DIMENSIONS[e.name]) ?? []))
      return { info: parseWorldInfo(root.tag), dims: Object.values(DIMENSIONS).filter((d) => found.has(d)) }
    },
    staleTime: 60_000,
    retry: false,
  })
}

export function WorldsTab({ server, status }: { server: Server; status: StatusKey }) {
  const qc = useQueryClient()
  const live = isLiveStatus(status)
  const tree = useQuery({
    queryKey: ['file-tree', server.id, '/', 2, 'sizes'],
    queryFn: () => api.files.tree(server.id, '/', { depth: 2, max: 20000, sizes: true }),
  })
  const root = useQuery({ queryKey: ['files', server.id, '/'], queryFn: () => api.files.list(server.id, '/') })
  const propsFile = useQuery({ queryKey: ['file-content', server.id, '/server.properties'], queryFn: () => api.files.readContent(server.id, '/server.properties'), retry: false })
  const props = useMemo(() => parseProperties(propsFile.data ?? ''), [propsFile.data])
  const loadedName = props['level-name']?.trim() || 'world'
  const worlds = useMemo(() => findWorlds(tree.data, loadedName), [tree.data, loadedName])
  const loaded = worlds.find((w) => w.folder === loadedName)
  const [upload, setUpload] = useState<{ name?: string } | null>(null)

  // What a change touched reloads: the folder list always, a world's own
  // details only when it was written (a deleted one has nothing to read).
  const refresh = (rewrote?: string) => {
    void qc.invalidateQueries({ queryKey: ['file-tree', server.id] })
    void qc.invalidateQueries({ queryKey: ['files', server.id] })
    void qc.invalidateQueries({ queryKey: ['file-content', server.id, '/server.properties'] })
    if (rewrote) void qc.invalidateQueries({ queryKey: ['world', server.id, rewrote] })
  }
  const restartAction = live ? { actions: [{ label: 'Restart now', run: () => void act(`restart ${server.name}`, () => api.servers.restart(server.id)) }] } : undefined

  const load = (w: WorldRow) =>
    void act(`load ${w.folder}`, async () => {
      // The file as it is now, not as this page loaded it: only level-name changes.
      const text = await api.files.readContent(server.id, '/server.properties').catch(() => defaultServerPropertiesFor(server.port))
      await api.files.writeContent(server.id, '/server.properties', serializeProperties(text, { 'level-name': w.folder }))
      return true
    }).then((r) => {
      if (r === undefined) return
      toast('ok', `${w.folder} loads next`, live ? 'It takes over after a restart.' : 'It loads the next time the server starts.', restartAction)
      void qc.invalidateQueries({ queryKey: ['file-content', server.id, '/server.properties'] })
    })

  const remove = async (w: WorldRow, reset: boolean) => {
    const also = w.linked.length ? `, with its Nether and End,` : ''
    const ok = await ask({
      title: reset ? `Reset ${w.folder}?` : `Delete ${w.folder}?`,
      body: reset
        ? `It’s deleted${also} and a new world is generated the next time ${server.name} starts. The server is backed up first.`
        : `It’s deleted${also} for good. The server is backed up first, so it can be restored from Backups.`,
      confirm: reset ? 'Back up and reset' : 'Back up and delete',
      danger: true,
      typeToConfirm: w.folder,
    })
    if (!ok) return
    const r = await act(reset ? `reset ${w.folder}` : `delete ${w.folder}`, async () => {
      toast('info', `Backing up ${server.name} first`)
      const b = await api.backups.create(server.id)
      await api.operations.wait(server.id, b.operation)
      for (const f of [w.folder, ...w.linked]) await api.files.delete(server.id, `/${f}`)
      return true
    })
    if (r === undefined) return
    toast('ok', reset ? `Reset ${w.folder}` : `Deleted ${w.folder}`, reset ? `A new one is made when ${server.name} next starts.` : 'The backup has it if you need it back.')
    refresh()
  }

  return (
    <>
      <div className="toolbar" style={{ marginBottom: 18 }}>
        <p className="t2 grow">One world loads at a time. Switching takes effect after a restart.</p>
        <Btn need="files.write" onClick={() => setUpload({})}>
          Upload a world
        </Btn>
      </div>
      <Loaded query={tree} sk={<SkTable rows={2} />} what={`the worlds on ${server.name}`}>
        {() =>
          worlds.length === 0 ? (
            <Empty title="No world yet.">One is made the first time the server starts, or you can upload one.</Empty>
          ) : (
            <table className="t worlds-t">
              <thead>
                <tr>
                  <th>Folder</th>
                  <th className="r" style={{ width: 110 }}>
                    Size
                  </th>
                  <th style={{ width: 150 }}>Last played</th>
                  <th style={{ width: 90 }} />
                  <th style={{ width: 48 }} />
                </tr>
              </thead>
              <tbody>
                {worlds.map((w) => (
                  <WorldRowView
                    key={w.folder}
                    server={server}
                    w={w}
                    isLoaded={w === loaded}
                    live={live}
                    onLoad={() => load(w)}
                    onReplace={() => setUpload({ name: w.folder })}
                    onReset={() => void remove(w, true)}
                    onDelete={() => void remove(w, false)}
                  />
                ))}
              </tbody>
            </table>
          )
        }
      </Loaded>
      {loaded && <LoadedWorld server={server} w={loaded} status={status} props={props} />}
      {upload && (
        <WorldUploadDialog
          server={server}
          live={live}
          initialName={upload.name}
          worlds={worlds.map((w) => w.folder)}
          folders={(root.data?.entries ?? []).filter((e) => e.type === 'dir').map((e) => e.name)}
          loaded={loadedName}
          onClose={() => setUpload(null)}
          onDone={(name) => refresh(name)}
        />
      )}
    </>
  )
}

function WorldRowView({
  server,
  w,
  isLoaded,
  live,
  onLoad,
  onReplace,
  onReset,
  onDelete,
}: {
  server: Server
  w: WorldRow
  isLoaded: boolean
  live: boolean
  onLoad: () => void
  onReplace: () => void
  onReset: () => void
  onDelete: () => void
}) {
  const world = useWorld(server.id, w)
  const info = world.data?.info
  const sub = [world.data?.dims.join(', '), info?.version && `Minecraft ${info.version}${info.snapshot ? ' snapshot' : ''}`].filter(Boolean).join(', ')
  const played = info?.lastPlayed ?? w.saved
  return (
    <tr>
      <td>
        <span className="strong mono">{w.folder}</span>
        {isLoaded && <span className="t3 small"> loaded</span>}
        <div className="sub">{world.isLoading ? <Sk w={180} h={12} /> : world.isError ? 'Couldn’t read its level.dat.' : sub || '–'}</div>
      </td>
      <td className="r num t2">{w.size != null ? fmtBytes(w.size) : '–'}</td>
      <td className="t2">{isLoaded && live ? 'Now' : played ? relTime(played) : '–'}</td>
      <td className="actions">
        {!isLoaded && (
          <Btn size="sm" need="files.write" onClick={onLoad}>
            Load
          </Btn>
        )}
      </td>
      <td className="actions">
        <MenuButton
          label={`More for ${w.folder}`}
          items={(close) => (
            <>
              <MenuItem close={close} need="files.read" onClick={() => void act(`download ${w.folder}`, () => api.files.download(server.id, `/${w.folder}`))}>
                Download
              </MenuItem>
              <MenuItem close={close} need="files.write" disabled={isLoaded && live} onClick={onReplace}>
                {isLoaded && live ? 'Replace (stop the server first)' : 'Replace with an upload'}
              </MenuItem>
              <MenuSep />
              <MenuItem danger close={close} need="files.delete" disabled={isLoaded && live} onClick={onReset}>
                {isLoaded && live ? 'Reset (stop the server first)' : 'Reset'}
              </MenuItem>
              {!isLoaded && (
                <MenuItem danger close={close} need="files.delete" onClick={onDelete}>
                  Delete
                </MenuItem>
              )}
            </>
          )}
        />
      </td>
    </tr>
  )
}

// ---- the loaded world -------------------------------------------------------------------------

function LoadedWorld({ server, w, status, props }: { server: Server; w: WorldRow; status: StatusKey; props: Record<string, string> }) {
  const world = useWorld(server.id, w)
  const info = world.data?.info
  return (
    <section className="section">
      <div className="section-head">
        <h2 className="section-title">
          {w.folder}
          {info?.levelName && info.levelName !== w.folder && <span className="t3"> ({info.levelName})</span>}
        </h2>
      </div>
      {world.isLoading ? (
        <SkTable rows={4} />
      ) : !info ? (
        <p className="t2">Its level.dat can’t be read yet. The server writes it as it starts.</p>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))', gap: '24px 56px', alignItems: 'start' }}>
          <dl className="facts">
            {info.seed && (
              <>
                <dt>Seed</dt>
                <dd>
                  <Copy text={info.seed} mono />
                </dd>
              </>
            )}
            {info.type && (
              <>
                <dt>Type</dt>
                <dd>{info.type}</dd>
              </>
            )}
            {info.spawn && (
              <>
                <dt>Spawn</dt>
                <dd>
                  <Copy text={info.spawn.join(' ')} mono />
                </dd>
              </>
            )}
            <dt>Difficulty</dt>
            <dd>
              {cap(props.difficulty || 'easy')}
              {props.hardcore === 'true' || info.hardcore ? ', hardcore' : ''}
            </dd>
            {info.gameMode && (
              <>
                <dt>Game mode</dt>
                <dd>
                  {info.gameMode}
                  {info.cheats ? ', cheats on' : ''}
                </dd>
              </>
            )}
            {info.day != null && (
              <>
                <dt>In-game day</dt>
                <dd className="num">
                  {info.day.toLocaleString('en-US')}
                  {info.clock ? `, ${info.clock}` : ''}
                  {info.weather ? `, ${info.weather.toLowerCase()}` : ''}
                </dd>
              </>
            )}
            {info.dataVersion != null && (
              <>
                <dt>Data version</dt>
                <dd className="num">{info.dataVersion}</dd>
              </>
            )}
          </dl>
          <GameRules server={server} status={status} rules={info.gameRules} />
        </div>
      )}
    </section>
  )
}

/** Game rules as last saved, changed with the gamerule command while the
 *  server runs (the world file can't be edited safely under it). */
function GameRules({ server, status, rules }: { server: Server; status: StatusKey; rules: WorldInfo['gameRules'] }) {
  const [set, setSet] = useState<Record<string, boolean | number | string>>({})
  const [q, setQ] = useState('')
  const can = useCan()
  const online = status === 'online'
  const all = { ...rules, ...set }
  const keys = Object.keys(all)
    .sort((a, b) => a.localeCompare(b))
    .filter((k) => !q || k.toLowerCase().includes(q.toLowerCase()))
  if (Object.keys(rules).length === 0) return null
  const apply = (k: string, v: boolean | number) =>
    void act(`set ${k}`, () => api.servers.command(server.id, `gamerule ${k} ${v}`)).then((r) => {
      if (r === undefined) return
      setSet((s) => ({ ...s, [k]: v }))
    })
  return (
    <div>
      <div className="row" style={{ marginBottom: 4, gap: 12 }}>
        <span className="field-label grow">Game rules</span>
        {Object.keys(all).length > 10 && <Search value={q} onChange={setQ} placeholder="Filter" size="sm" style={{ width: 160 }} />}
      </div>
      <div style={{ maxHeight: 420, overflowY: 'auto' }}>
        {keys.map((k) => {
          const v = all[k]
          return (
            <div key={k} className="setting" style={{ padding: '8px 0' }}>
              <span className="text mono small t2" style={{ fontWeight: 400 }}>
                {k.replace(/^minecraft:/, '')}
              </span>
              {typeof v === 'boolean' ? (
                <Switch checked={v} label={k} need="console" disabled={!online} onChange={(nv) => apply(k, nv)} />
              ) : typeof v === 'number' ? (
                <RuleNumber value={v} label={k} disabled={!online || !can('console')} onCommit={(nv) => apply(k, nv)} />
              ) : (
                <span className="num t2">{v}</span>
              )}
            </div>
          )
        })}
        {keys.length === 0 && <p className="t3 small">No rules match.</p>}
      </div>
      <p className="t3 small" style={{ marginTop: 6 }}>{online ? 'Changes apply straight away.' : 'Start the server to change game rules.'}</p>
    </div>
  )
}

function RuleNumber({ value, label, disabled, onCommit }: { value: number; label: string; disabled: boolean; onCommit: (v: number) => void }) {
  const [d, setD] = useState(String(value))
  useEffect(() => setD(String(value)), [value])
  const commit = () => {
    const n = Number(d)
    if (d.trim() === '' || !Number.isInteger(n)) setD(String(value))
    else if (n !== value) onCommit(n)
  }
  return (
    <input
      className="input sm num"
      inputMode="numeric"
      value={d}
      disabled={disabled}
      aria-label={label}
      style={{ width: 96 }}
      onChange={(e) => setD(e.target.value.replace(/[^\d-]/g, ''))}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
        if (e.key === 'Escape') setD(String(value))
      }}
    />
  )
}

// ---- uploading ----------------------------------------------------------------------------------

// Characters the agent refuses in a world folder name, checked here before an
// upload is spent on one. Spaces are fine: plenty of downloaded maps have them.
const BAD_CHARS = /[/\\:*?"<>|]|\p{Cc}/u
const BAD_CHARS_G = /[/\\:*?"<>|]|\p{Cc}/gu

/** "My World (1).zip" → "My World". */
export function folderFromZip(fileName: string): string {
  return fileName
    .replace(/\.zip$/i, '')
    .replace(/\s*\(\d+\)$/, '')
    .replace(BAD_CHARS_G, ' ')
    .trim()
    .slice(0, 64)
}

export function folderError(name: string): string | null {
  const n = name.trim()
  if (!n) return 'Give it a folder name.'
  if (n.length > 64) return 'Keep it to 64 characters or fewer.'
  if (n.startsWith('.') || n.endsWith('.')) return 'It can’t start or end with a dot.'
  if (BAD_CHARS.test(n)) return 'It can’t contain / \\ : * ? " < > or |.'
  return null
}

const MB = 1024 * 1024

function WorldUploadDialog({
  server,
  live,
  initialName,
  worlds,
  folders,
  loaded,
  onClose,
  onDone,
}: {
  server: Server
  live: boolean
  initialName?: string
  worlds: string[]
  folders: string[]
  loaded: string
  onClose: () => void
  onDone: (name: string) => void
}) {
  const [file, setFile] = useState<File | null>(null)
  const [name, setName] = useState(initialName ?? '')
  const [loadIt, setLoadIt] = useState(true)
  const [over, setOver] = useState(false)
  const [phase, setPhase] = useState<'idle' | 'sending' | 'unpacking'>('idle')
  const [sentFor, setSentFor] = useState(0)
  const stats = useUploadStats()
  const ref = useRef<HTMLInputElement>(null)
  const abort = useRef<AbortController | null>(null)
  const started = useRef(0)

  const trimmed = name.trim()
  const same = (a: string) => a.toLowerCase() === trimmed.toLowerCase()
  const replacing = folders.some(same)
  const replacingWorld = worlds.some(same)
  const isLoaded = !!trimmed && same(loaded)
  const blocked = live && isLoaded
  const invalid = file ? folderError(name) : null
  const busy = phase !== 'idle'

  const take = (f: File | undefined | null) => {
    if (!f) return
    if (!/\.zip$/i.test(f.name)) {
      toast('bad', `${f.name} isn’t a .zip`, 'Zip the world folder first.')
      return
    }
    setFile(f)
    setName((n) => n || folderFromZip(f.name))
  }

  const go = async () => {
    if (!file || invalid || blocked) return
    const ctl = new AbortController()
    abort.current = ctl
    started.current = Date.now()
    stats.begin()
    setPhase('sending')
    try {
      const result = await api.worlds.upload(
        server.id,
        file,
        { name: trimmed, overwrite: replacing, signal: ctl.signal },
        stats.onProgress,
        () => {
          setSentFor((t) => t || Date.now() - started.current)
          setPhase('unpacking')
        },
      )
      const makeLoaded = loadIt && !isLoaded
      if (makeLoaded) {
        const text = await api.files.readContent(server.id, '/server.properties').catch(() => defaultServerPropertiesFor(server.port))
        await api.files.writeContent(server.id, '/server.properties', serializeProperties(text, { 'level-name': result.name }))
      }
      toast(
        'ok',
        result.replaced ? `Replaced ${result.name}` : `Uploaded ${result.name}`,
        makeLoaded || isLoaded ? (live ? 'It loads after a restart.' : 'It loads the next time the server starts.') : `${result.files.toLocaleString('en-US')} files, ${fmtBytes(result.bytes)}.`,
      )
      onDone(result.name)
      onClose()
    } catch (e) {
      if (ctl.signal.aborted) {
        toast('info', 'Stopped the upload', 'Nothing on the server changed.')
      } else {
        const f = failureOf(e)
        toast('bad', `Couldn’t upload ${trimmed}`, f.message)
      }
      setPhase('idle')
      stats.clear()
    } finally {
      abort.current = null
    }
  }

  const st = stats.stats
  const tp = st?.throughput
  const stalled = !!tp && phase === 'sending' && Date.now() - started.current > 3000 && tp.bps < tp.avgBps * 0.1
  return (
    <Dialog
      open
      onClose={busy ? () => {} : onClose}
      title={initialName ? `Replace ${initialName}` : 'Upload a world'}
      foot={
        busy ? (
          <>
            <span className="left t3 small">{phase === 'unpacking' ? 'Almost done.' : 'Keep this open until it finishes.'}</span>
            <Btn variant="quiet" disabled={phase === 'unpacking'} onClick={() => abort.current?.abort()}>
              Stop upload
            </Btn>
          </>
        ) : (
          <>
            <Btn variant="quiet" onClick={onClose}>
              Cancel
            </Btn>
            <Btn variant={replacing ? 'danger primary' : 'primary'} need="files.write" disabled={!file || !!invalid || blocked} onClick={() => void go()}>
              {replacing ? 'Replace it' : 'Upload'}
            </Btn>
          </>
        )
      }
    >
      <div className="stack" style={{ gap: 14 }}>
        <input
          ref={ref}
          type="file"
          accept=".zip,application/zip"
          hidden
          onChange={(e) => {
            take(e.target.files?.[0])
            e.target.value = ''
          }}
        />
        {!busy && (
          <>
            <button
              type="button"
              className={cx('drop', over && 'over')}
              onClick={() => ref.current?.click()}
              onDragOver={(e) => {
                e.preventDefault()
                setOver(true)
              }}
              onDragLeave={() => setOver(false)}
              onDrop={(e) => {
                e.preventDefault()
                setOver(false)
                take(e.dataTransfer.files?.[0])
              }}
            >
              {file ? (
                <>
                  <b>{file.name}</b>
                  <div className="small t3">{fmtBytes(file.size)}. Click to choose another.</div>
                </>
              ) : (
                <>
                  Drop a .zip of a world here, or click to choose one.
                  <div className="small t3" style={{ marginTop: 4 }}>
                    The world folder, a folder around it, or a whole server backup: whatever holds level.dat is what’s installed.
                  </div>
                </>
              )}
            </button>
            {file && (
              <Field
                label="Folder name"
                error={
                  invalid ??
                  (blocked
                    ? `${server.name} is running with ${loaded} open. Stop it first, or choose another name.`
                    : replacing
                      ? replacingWorld
                        ? `There’s already a ${trimmed}. It’s replaced entirely, so back it up first if you want it.`
                        : `${trimmed} is a folder that isn’t a world. Uploading deletes it and everything in it.`
                      : null)
                }
              >
                <input className="input mono" value={name} onChange={(e) => setName(e.target.value)} aria-label="Folder name" />
              </Field>
            )}
            {file && !isLoaded && <Check checked={loadIt} onChange={setLoadIt} label={live ? 'Load it after the next restart' : 'Load it next time the server starts'} />}
          </>
        )}
        {busy && file && (
          <div className="upload-live" aria-live="polite">
            <div className="row" style={{ alignItems: 'baseline', gap: 10 }}>
              <span className="mono strong ellipsis grow">{file.name}</span>
              <span className="t3 small num">
                {fmtBytes(phase === 'unpacking' ? file.size : (st?.loaded ?? 0))} of {fmtBytes(file.size)}
              </span>
            </div>
            <Meter value={phase === 'unpacking' ? 1 : (st?.loaded ?? 0) / file.size} max={1} busy={phase === 'unpacking'} label="Upload progress" />
            <div className="upload-figures">
              <div>
                {phase === 'unpacking' ? (
                  <>
                    <div className="big num">
                      {tp ? (tp.avgBps / MB).toFixed(1) : '–'}
                      <small>MB/s on average</small>
                    </div>
                    <div className="t2 small">
                      Sent in {fmtDur(Math.max(1000, sentFor), 2)}. Unpacking on the node…
                    </div>
                  </>
                ) : (
                  <>
                    <div className={cx('big num', stalled && 'warn')}>
                      {tp ? (tp.bps / MB).toFixed(1) : '0.0'}
                      <small>MB/s now</small>
                    </div>
                    <div className={cx('small', stalled ? 'warn' : 't2')}>
                      {!tp || !tp.history.length ? 'Starting…' : stalled ? 'Stalled. Waiting for the connection to pick up.' : st?.etaSec != null ? `About ${fmtDur(Math.max(1000, st.etaSec * 1000), 2)} left` : ''}
                    </div>
                  </>
                )}
              </div>
              <div className="upload-spark" data-tip="Speed over the last few seconds">
                <Spark fluid values={tp && tp.history.length > 1 ? tp.history : [0, 0]} w={200} h={40} min={0} />
              </div>
            </div>
            {tp && tp.history.length > 0 && (
              <div className="row t3 small" style={{ gap: 18 }}>
                <span>Average {(tp.avgBps / MB).toFixed(1)} MB/s</span>
                <span>Peak {(tp.peakBps / MB).toFixed(1)} MB/s</span>
                <span>{fmtDur(Math.max(1000, (phase === 'unpacking' ? sentFor : Date.now() - started.current)), 2)} so far</span>
              </div>
            )}
          </div>
        )}
      </div>
    </Dialog>
  )
}
