import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { detectFormat, hasEditableContent, humanizeFilename, isUrl, parseConfig, pathKey, reindex, saveConfig, valueToTree, type ConfigFormat, type ConfigNode, type ParsedConfig, type PathKey, type Primitive } from '@/lib/config'
import type { Server } from '@/lib/types'
import { Btn, Empty, Field, Filters, IconBtn, Notice, Search, Switch } from '@/ui/controls'
import { act } from '@/ui/failure'
import { Icon } from '@/ui/icon'
import { Dialog } from '@/ui/overlays'
import { useCan } from '@/ui/perm'
import { FilesSkeleton } from '@/ui/skeleton'
import { toast } from '@/ui/toasts'
import { cx, plural } from '@/ui/util'
import { CodeEditor } from './code-editor'
import { okToOverwrite } from './data'

// Mod and plugin config files as forms: every option with the comment that
// explains it, edited in place so comments and formatting survive, with the
// raw text a click away. A search looks through file names and the options
// inside them.

const CONFIG_EXTS = new Set(['json', 'json5', 'jsonc', 'toml', 'yaml', 'yml', 'properties', 'conf', 'hocon', 'cfg', 'ini', 'txt'])
/** Where mods and plugins keep their configuration. */
const CONFIG_ROOTS = ['/config', '/defaultconfigs', '/world/serverconfig', '/plugins']
const MAX_FILES = 3000
const MAX_DEPTH = 8

export interface ConfigFile {
  path: string
  name: string
  dir: string
  ext: string
}

/** Config files among a root's tree entries. Plugins keep theirs one folder down. */
export function configFilesIn(root: string, entries: { path: string; type: string }[]): ConfigFile[] {
  const out: ConfigFile[] = []
  for (const e of entries) {
    if (e.type !== 'file') continue
    if (root === '/plugins' && !e.path.includes('/')) continue
    const full = `${root}/${e.path}`
    const slash = full.lastIndexOf('/')
    const name = full.slice(slash + 1)
    const ext = name.split('.').pop()?.toLowerCase() ?? ''
    if (CONFIG_EXTS.has(ext)) out.push({ path: full, name, dir: full.slice(0, slash), ext })
  }
  return out
}

async function discover(serverId: string): Promise<ConfigFile[]> {
  // One walk per root, in parallel; a missing root just isn't there.
  // Plugins keep their config one folder down; deeper is their data (map
  // tiles, player files), which isn't configuration.
  const trees = await Promise.all(CONFIG_ROOTS.map((root) => api.files.tree(serverId, root, { depth: root === '/plugins' ? 2 : MAX_DEPTH, max: MAX_FILES }).catch(() => null)))
  const out = trees.flatMap((t, i) => (t ? configFilesIn(CONFIG_ROOTS[i], t.entries) : []))
  return out.slice(0, MAX_FILES).sort((a, b) => a.path.localeCompare(b.path))
}

interface Option {
  file: ConfigFile
  node: PathKey[]
  label: string
  preview: string
  hay: string
}

const MAX_OPTIONS = 20000

function collect(node: ConfigNode, file: ConfigFile, out: Option[]) {
  if (out.length >= MAX_OPTIONS) return
  const box = node.type === 'object' || node.type === 'array'
  const label = node.key !== null ? String(node.key) : node.index !== null ? `#${node.index + 1}` : ''
  const preview = box ? '' : String(node.value ?? '')
  if (label && node.path.length) out.push({ file, node: node.path, label, preview, hay: `${label} ${preview} ${node.doc ?? ''}`.toLowerCase() })
  if (box) for (const c of node.children ?? []) collect(c, file, out)
}

async function indexOptions(serverId: string, files: ConfigFile[]): Promise<Option[]> {
  const out: Option[] = []
  let next = 0
  const worker = async () => {
    while (next < files.length && out.length < MAX_OPTIONS) {
      const file = files[next++]
      try {
        const parsed = parseConfig(file.name, await api.files.readContent(serverId, file.path))
        if (parsed) collect(parsed.root, file, out)
      } catch {
        // An unreadable file just isn't searched.
      }
    }
  }
  // A few at a time, so a big pack doesn't fire hundreds of requests at once.
  await Promise.all(Array.from({ length: Math.min(8, files.length) }, worker))
  return out
}

export function ConfigsView({ server }: { server: Server }) {
  const files = useQuery({ queryKey: ['config-files', server.id], queryFn: () => discover(server.id), staleTime: 5 * 60_000, gcTime: 30 * 60_000 })
  const [open, setOpen] = useState<string | null>(null)
  const [target, setTarget] = useState<{ path: string; node: PathKey[]; n: number } | null>(null)
  const select = (path: string, node?: PathKey[]) => {
    setOpen(path)
    setTarget(node ? { path, node, n: Date.now() } : null)
  }
  return (
    <div className={cx('configs', open && 'has-file')}>
      <div className="configs-list">
        {files.isLoading ? (
          <FilesSkeleton />
        ) : files.isError ? (
          <Empty title="Couldn’t look for config files." action={<Btn onClick={() => void files.refetch()}>Try again</Btn>} />
        ) : !files.data?.length ? (
          <Empty title="No config files yet.">Mods and plugins write theirs the first time the server starts with them.</Empty>
        ) : (
          <FileList server={server} files={files.data} open={open} onSelect={select} onRescan={() => void files.refetch()} rescanning={files.isFetching} />
        )}
      </div>
      <div className="configs-editor">
        {open ? (
          <ConfigEditor key={open} server={server} path={open} target={target?.path === open ? target : null} onBack={() => setOpen(null)} />
        ) : (
          <Empty title="Pick a config file.">Options show as a form with what each one does. You can switch to the raw text at any time.</Empty>
        )}
      </div>
    </div>
  )
}

function FileList({ server, files, open, onSelect, onRescan, rescanning }: { server: Server; files: ConfigFile[]; open: string | null; onSelect: (path: string, node?: PathKey[]) => void; onRescan: () => void; rescanning: boolean }) {
  const [q, setQ] = useState('')
  const term = q.trim().toLowerCase()
  const searching = term.length >= 2
  const options = useQuery({ queryKey: ['config-index', server.id, files.length], queryFn: () => indexOptions(server.id, files), enabled: searching, staleTime: 60_000 })
  const groups = useMemo(() => {
    const by = new Map<string, ConfigFile[]>()
    for (const f of files) by.set(f.dir, [...(by.get(f.dir) ?? []), f])
    return [...by.entries()]
  }, [files])
  const fileHits = term ? files.filter((f) => f.path.toLowerCase().includes(term)) : []
  const optionHits = searching
    ? (options.data ?? [])
        .map((o) => ({ o, rank: o.label.toLowerCase().includes(term) ? 0 : o.hay.includes(term) ? 1 : 2 }))
        .filter((x) => x.rank < 2)
        .sort((a, b) => a.rank - b.rank)
        .slice(0, 100)
        .map((x) => x.o)
    : []
  const row = (f: ConfigFile, showDir?: boolean) => (
    <button key={f.path} type="button" className={cx('cfg-file', open === f.path && 'current')} onClick={() => onSelect(f.path)}>
      <span className="ellipsis grow">
        {f.name}
        {showDir && <span className="t3 small"> {f.dir.replace(/^\//, '')}</span>}
      </span>
      <span className="t3 tiny">{f.ext}</span>
    </button>
  )
  return (
    <>
      <div className="row" style={{ gap: 6, padding: '12px 12px 8px' }}>
        <Search value={q} onChange={setQ} placeholder="Search files and options" size="sm" style={{ flex: 1 }} />
        <IconBtn icon="refresh" label="Look again" loading={rescanning} onClick={onRescan} />
      </div>
      <p className="t3 tiny" style={{ padding: '0 14px 6px' }}>
        {searching ? `${plural(fileHits.length, 'file')}, ${plural(optionHits.length, 'option')}${options.isFetching ? ', searching inside…' : ''}` : plural(files.length, 'config file')}
      </p>
      <div className="configs-scroll">
        {!searching ? (
          groups.map(([dir, items]) => (
            <Fragment key={dir}>
              <div className="cfg-dir">{dir.replace(/^\//, '')}</div>
              {items.map((f) => row(f))}
            </Fragment>
          ))
        ) : (
          <>
            {fileHits.length > 0 && (
              <>
                <div className="cfg-dir">Files</div>
                {fileHits.map((f) => row(f, true))}
              </>
            )}
            <div className="cfg-dir">Inside them</div>
            {optionHits.length === 0 && !options.isFetching && <p className="t3 small" style={{ padding: '4px 10px' }}>No options match.</p>}
            {optionHits.map((o, i) => (
              <button key={`${o.file.path}|${o.node.join('.')}|${i}`} type="button" className="cfg-file option" onClick={() => onSelect(o.file.path, o.node)}>
                <span className="ellipsis">
                  <span className="strong">{o.label}</span>
                  {o.preview && <span className="mono t3 small"> = {o.preview.slice(0, 32)}</span>}
                </span>
                <span className="t3 tiny ellipsis">
                  {o.file.name}
                  {o.node.length > 1 ? ` › ${o.node.slice(0, -1).join(' › ')}` : ''}
                </span>
              </button>
            ))}
          </>
        )}
      </div>
    </>
  )
}

function ConfigEditor({ server, path, target, onBack }: { server: Server; path: string; target: { node: PathKey[]; n: number } | null; onBack: () => void }) {
  const qc = useQueryClient()
  const can = useCan()
  const name = path.split('/').pop() ?? path
  const format = detectFormat(name)
  const content = useQuery({ queryKey: ['file-content', server.id, path], queryFn: () => api.files.readContent(server.id, path), retry: false, staleTime: 30_000 })
  const [base, setBase] = useState('')
  const [parsed, setParsed] = useState<ParsedConfig | null>(null)
  const [mode, setMode] = useState<'form' | 'raw'>('form')
  const [raw, setRaw] = useState('')
  const [dirty, setDirty] = useState(false)
  const [filter, setFilter] = useState('')
  const [saving, setSaving] = useState(false)
  const [rev, setRev] = useState(0)
  const [, setTick] = useState(0)

  // Start over whenever the file's text arrives, unless that would throw away
  // edits: then saving asks first (see okToOverwrite).
  const dirtyNow = useRef(false)
  dirtyNow.current = dirty
  const opened = useRef('')
  useEffect(() => {
    if (content.data === undefined || dirtyNow.current) return
    opened.current = content.data
    const p = parseConfig(name, content.data)
    setBase(content.data)
    setRaw(content.data)
    setParsed(p)
    setMode(p && hasEditableContent(p.root) ? 'form' : 'raw')
    setDirty(false)
    setFilter('')
    setRev((n) => n + 1)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [content.data])
  useEffect(() => {
    if (target) setFilter('')
  }, [target])

  const formOk = parsed !== null && hasEditableContent(parsed.root)
  const text = () => (mode === 'form' && parsed ? saveConfig(parsed, base) : raw)
  const toRaw = () => {
    setRaw(text())
    setMode('raw')
    setRev((n) => n + 1)
  }
  const toForm = () => {
    const p = parseConfig(name, raw)
    if (!p) {
      toast('bad', `That isn’t valid ${format} yet`, 'Fix it in the raw text, or keep editing there.')
      return
    }
    setBase(raw)
    setParsed(p)
    setMode('form')
    setRev((n) => n + 1)
  }
  const save = async () => {
    if (!dirty || saving) return
    const next = text()
    setSaving(true)
    if (!(await okToOverwrite(server.id, path, opened.current, next))) return setSaving(false)
    const r = await act(`save ${name}`, () => api.files.writeContent(server.id, path, next).then(() => true))
    setSaving(false)
    if (!r) return
    opened.current = next
    qc.setQueryData(['file-content', server.id, path], next)
    setDirty(false)
    toast('ok', `Saved ${name}`, 'Most mods read their config when the server starts.')
  }

  return (
    <>
      <div className="file-head">
        <IconBtn icon="arrow-l" label="Back to the list" className="show-sm" onClick={onBack} />
        <span className="mono small ellipsis grow">
          {path}
          {dirty && <span className="t3"> (edited)</span>}
        </span>
        {mode === 'form' && formOk && <Search value={filter} onChange={setFilter} placeholder="Filter" size="sm" style={{ width: 150 }} />}
        <Filters
          value={mode}
          onChange={(v) => (v === 'raw' ? toRaw() : formOk || parseConfig(name, raw) ? toForm() : undefined)}
          label="Show as"
          options={[
            { value: 'form', label: 'Form' },
            { value: 'raw', label: 'Raw' },
          ]}
        />
        <IconBtn icon="refresh" label="Read it again" onClick={() => void content.refetch()} />
        <Btn size="sm" variant="primary" need="files.write" disabled={!dirty} loading={saving} onClick={() => void save()}>
          Save
        </Btn>
      </div>
      {content.data !== undefined && parsed === null && (
        <div style={{ padding: '10px 14px 0' }}>
          <Notice tone="warn">This can’t be read as {format === 'unknown' ? 'a config' : format}, so it’s shown as text.</Notice>
        </div>
      )}
      <div className="file-body">
        {content.isLoading ? (
          <FilesSkeleton />
        ) : content.isError ? (
          <Empty title={`Couldn’t open ${name}.`} action={<Btn onClick={() => void content.refetch()}>Try again</Btn>} />
        ) : mode === 'form' && parsed && formOk ? (
          <div className="cfg-form">
            {!parsed.structural && <p className="t3 small" style={{ marginBottom: 12 }}>Values are changed in place, so comments and formatting stay as they are.</p>}
            <ConfigForm
              key={rev}
              root={parsed.root}
              format={parsed.format}
              structural={parsed.structural}
              filter={filter}
              filename={name}
              readOnly={!can('files.write')}
              onChange={() => {
                setDirty(true)
                setTick((n) => n + 1)
              }}
              highlight={target ? pathKey(target.node) : undefined}
              highlightNonce={target?.n}
            />
          </div>
        ) : (
          <div className="cfg-raw">
            <CodeEditor
              key={rev}
              path={path}
              doc={raw}
              readOnly={!can('files.write')}
              onChange={(v) => {
                setRaw(v)
                setDirty(true)
              }}
              onSave={() => void save()}
            />
          </div>
        )}
      </div>
    </>
  )
}

// ---- the form ------------------------------------------------------------------------------------

interface Ctx {
  format: ConfigFormat
  structural: boolean
  readOnly: boolean
  filter: string
  bump: () => void
  highlight?: string
  highlightNonce?: number
  askKey: (taken: string[]) => Promise<string | null>
}

const labelOf = (n: ConfigNode) => (n.key !== null ? String(n.key) : n.index !== null ? `#${n.index + 1}` : '')

export function matchesFilter(n: ConfigNode, q: string): boolean {
  if (!q) return true
  if (String(n.key ?? '').toLowerCase().includes(q) || n.doc?.toLowerCase().includes(q)) return true
  return (n.children ?? []).some((c) => matchesFilter(c, q))
}

function defaultFor(siblings: ConfigNode[]): Primitive {
  if (siblings.length && siblings.every((c) => c.type === 'boolean')) return false
  if (siblings.length && siblings.every((c) => c.type === 'number')) return 0
  return ''
}

/** Scrolls a search target into view and flashes it. */
function useHighlight(ctx: Ctx, n: ConfigNode) {
  const ref = useRef<HTMLDivElement>(null)
  const [flash, setFlash] = useState(false)
  const hit = ctx.highlight !== undefined && ctx.highlight === pathKey(n.path)
  useEffect(() => {
    if (!hit || !ref.current) return
    ref.current.scrollIntoView({ block: 'center', behavior: 'smooth' })
    setFlash(true)
    const t = setTimeout(() => setFlash(false), 1800)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctx.highlight, ctx.highlightNonce])
  return { ref, flash }
}

function Scalar({ n, ctx, off }: { n: ConfigNode; ctx: Ctx; off?: boolean }) {
  const [text, setText] = useState(() => (n.value == null ? '' : String(n.value)))
  const disabled = ctx.readOnly || off
  if (isUrl(n.value)) {
    return (
      <a className="link quiet ellipsis small" href={String(n.value)} target="_blank" rel="noreferrer noopener" style={{ maxWidth: 420 }}>
        {String(n.value)}
      </a>
    )
  }
  if (!n.editable) {
    return (
      <span className="mono small t3 ellipsis" data-tip="This one can’t be edited in the form">
        {String(n.value)}
      </span>
    )
  }
  if (n.type === 'boolean') {
    return (
      <Switch
        checked={n.value === true}
        disabled={disabled}
        label={labelOf(n)}
        onChange={(v) => {
          n.value = v
          ctx.bump()
        }}
      />
    )
  }
  if (n.type === 'number') {
    return (
      <input
        className="input sm num"
        inputMode="decimal"
        value={text}
        disabled={disabled}
        aria-label={labelOf(n)}
        style={{ width: 140 }}
        onChange={(e) => {
          setText(e.target.value)
          const v = Number(e.target.value)
          if (e.target.value.trim() !== '' && !Number.isNaN(v)) {
            n.value = v
            ctx.bump()
          }
        }}
      />
    )
  }
  if (typeof n.value === 'string' && n.value.includes('\n')) {
    return (
      <textarea
        className="textarea mono"
        value={text}
        disabled={disabled}
        aria-label={labelOf(n)}
        rows={Math.min(8, text.split('\n').length + 1)}
        style={{ width: '100%', maxWidth: 560 }}
        onChange={(e) => {
          setText(e.target.value)
          n.value = e.target.value
          n.type = 'string'
          ctx.bump()
        }}
      />
    )
  }
  return (
    <input
      className="input sm mono"
      value={text}
      disabled={disabled}
      placeholder={n.type === 'null' ? 'null' : ''}
      aria-label={labelOf(n)}
      style={{ width: '100%', maxWidth: 420 }}
      onChange={(e) => {
        setText(e.target.value)
        if (n.type === 'null' && e.target.value === '') n.value = null
        else {
          n.value = e.target.value
          if (n.type === 'null') n.type = 'string'
        }
        ctx.bump()
      }}
    />
  )
}

const scalarList = (n: ConfigNode) => (n.children ?? []).every((c) => c.type !== 'object' && c.type !== 'array')

/** A map has id-like keys (namespace:name) rather than a fixed schema, so its
 *  entries can be added and removed. */
export function isMapLike(n: ConfigNode): boolean {
  const kids = n.children ?? []
  return kids.length > 0 && kids.some((c) => typeof c.key === 'string' && c.key.includes(':'))
}

function ListView({ n, ctx }: { n: ConfigNode; ctx: Ctx }) {
  const [, setTick] = useState(0)
  const kids = n.children ?? []
  const edit = ctx.structural && !ctx.readOnly
  const changed = () => {
    n.dirty = true
    reindex(n, n.path)
    ctx.bump()
    setTick((t) => t + 1)
  }
  const add = () => {
    if (kids.length && !scalarList(n)) kids.push(structuredClone(kids[kids.length - 1]))
    else kids.push(valueToTree(defaultFor(kids), null, kids.length, []))
    changed()
  }
  if (!kids.length) {
    return (
      <div className="row" style={{ gap: 12 }}>
        <span className="t3 small">Empty list</span>
        {edit && (
          <button type="button" className="link quiet small" onClick={add}>
            Add one
          </button>
        )}
      </div>
    )
  }
  if (scalarList(n)) {
    return (
      <div className="stack" style={{ gap: 6 }}>
        {kids.map((c, i) => (
          <div key={i} className="row cfg-item" style={{ gap: 8 }}>
            <span className="t3 small num" style={{ width: 22, textAlign: 'right' }}>
              {i + 1}
            </span>
            <Scalar n={c} ctx={ctx} />
            {edit && (
              <IconBtn
                icon="trash"
                label={`Remove item ${i + 1}`}
                className="cfg-remove"
                onClick={() => {
                  kids.splice(i, 1)
                  changed()
                }}
              />
            )}
          </div>
        ))}
        {edit && (
          <button type="button" className="link quiet small" style={{ alignSelf: 'flex-start' }} onClick={add}>
            Add an item
          </button>
        )}
      </div>
    )
  }
  return (
    <div className="stack" style={{ gap: 8 }}>
      {kids.map((c, i) => (
        <div key={i} className="cfg-card">
          <div className="row" style={{ gap: 6, marginBottom: 6 }}>
            <span className="t2 small strong grow">Item {i + 1}</span>
            {edit && (
              <>
                <IconBtn
                  icon="copy"
                  label={`Copy item ${i + 1}`}
                  onClick={() => {
                    kids.splice(i + 1, 0, structuredClone(kids[i]))
                    changed()
                  }}
                />
                <IconBtn
                  icon="trash"
                  label={`Remove item ${i + 1}`}
                  onClick={() => {
                    kids.splice(i, 1)
                    changed()
                  }}
                />
              </>
            )}
          </div>
          <Children n={c} ctx={ctx} />
        </div>
      ))}
      {edit && (
        <button type="button" className="link quiet small" style={{ alignSelf: 'flex-start' }} onClick={add}>
          Add an item
        </button>
      )}
    </div>
  )
}

function ObjectView({ n, ctx }: { n: ConfigNode; ctx: Ctx }) {
  const [, setTick] = useState(0)
  const kids = n.children ?? []
  const map = ctx.structural && !ctx.readOnly && isMapLike(n)
  const changed = () => {
    n.dirty = true
    reindex(n, n.path)
    ctx.bump()
    setTick((t) => t + 1)
  }
  return (
    <div className="stack" style={{ gap: 2 }}>
      {kids
        .filter((c) => matchesFilter(c, ctx.filter))
        .map((c, i) => (
          <Row
            key={`${c.key ?? i}`}
            n={c}
            ctx={ctx}
            onRemove={
              map
                ? () => {
                    kids.splice(kids.indexOf(c), 1)
                    changed()
                  }
                : undefined
            }
          />
        ))}
      {map && (
        <button
          type="button"
          className="link quiet small"
          style={{ alignSelf: 'flex-start', marginTop: 4 }}
          onClick={() =>
            void ctx.askKey(kids.map((c) => String(c.key))).then((key) => {
              if (!key) return
              kids.push(valueToTree(defaultFor(kids), key, null, []))
              changed()
            })
          }
        >
          Add an entry
        </button>
      )}
    </div>
  )
}

function Children({ n, ctx }: { n: ConfigNode; ctx: Ctx }) {
  return n.type === 'array' ? <ListView n={n} ctx={ctx} /> : <ObjectView n={n} ctx={ctx} />
}

function Row({ n, ctx, onRemove }: { n: ConfigNode; ctx: Ctx; onRemove?: () => void }) {
  const [open, setOpen] = useState(true)
  const [, setTick] = useState(0)
  const { ref, flash } = useHighlight(ctx, n)
  if (n.type === 'object' || n.type === 'array') {
    const count = n.children?.length ?? 0
    return (
      <div className="cfg-group">
        <div ref={ref} className={cx('row cfg-group-head', flash && 'flash')} style={{ gap: 4 }}>
          <button type="button" className="row grow" style={{ gap: 6, textAlign: 'left' }} aria-expanded={open} onClick={() => setOpen((o) => !o)}>
            <Icon name={open ? 'chev-d' : 'chev-r'} size={13} className="t3" />
            <span className="strong">{labelOf(n)}</span>
            <span className="t3 small">{n.type === 'array' ? plural(count, 'item') : plural(count, 'option')}</span>
          </button>
          {onRemove && <IconBtn icon="trash" label={`Remove ${labelOf(n)}`} className="cfg-remove" onClick={onRemove} />}
        </div>
        {n.doc && <p className="cfg-doc" style={{ marginLeft: 19 }}>{n.doc}</p>}
        {open && (
          <div className="cfg-children">
            <Children n={n} ctx={ctx} />
          </div>
        )}
      </div>
    )
  }
  const off = n.togglable === true && n.disabled === true
  return (
    <div ref={ref} className={cx('cfg-row', off && 'off', flash && 'flash')}>
      <div className="cfg-label">
        <div className="row" style={{ gap: 8 }}>
          {n.togglable && (
            <Switch
              checked={!n.disabled}
              label={`${labelOf(n)} on`}
              disabled={ctx.readOnly}
              onChange={() => {
                n.disabled = !n.disabled
                ctx.bump()
                setTick((t) => t + 1)
              }}
            />
          )}
          <span className="cfg-name">{labelOf(n)}</span>
        </div>
        {n.doc && <p className="cfg-doc">{n.doc}</p>}
      </div>
      <div className="row cfg-item" style={{ gap: 8, minWidth: 0, flex: 1 }}>
        <Scalar n={n} ctx={ctx} off={off} />
        {onRemove && <IconBtn icon="trash" label={`Remove ${labelOf(n)}`} className="cfg-remove" onClick={onRemove} />}
      </div>
    </div>
  )
}

function ConfigForm({
  root,
  format,
  structural,
  filter,
  filename,
  readOnly,
  onChange,
  highlight,
  highlightNonce,
}: {
  root: ConfigNode
  format: ConfigFormat
  structural: boolean
  filter: string
  filename: string
  readOnly: boolean
  onChange: () => void
  highlight?: string
  highlightNonce?: number
}) {
  const [asking, setAsking] = useState<{ taken: string[]; resolve: (v: string | null) => void } | null>(null)
  const ctx: Ctx = {
    format,
    structural,
    readOnly,
    filter: filter.trim().toLowerCase(),
    bump: onChange,
    highlight,
    highlightNonce,
    askKey: (taken) => new Promise((resolve) => setAsking({ taken, resolve })),
  }
  return (
    <>
      {root.type !== 'object' && root.type !== 'array' ? (
        // A file that's one bare value: its name is the label.
        <div className="cfg-row">
          <div className="cfg-label">
            <span className="cfg-name" style={{ textTransform: 'capitalize' }}>
              {humanizeFilename(filename)}
            </span>
            {root.doc && <p className="cfg-doc">{root.doc}</p>}
          </div>
          <Scalar n={root} ctx={ctx} />
        </div>
      ) : (
        <Children n={root} ctx={ctx} />
      )}
      {asking && (
        <KeyDialog
          taken={asking.taken}
          onDone={(v) => {
            asking.resolve(v)
            setAsking(null)
          }}
        />
      )}
    </>
  )
}

function KeyDialog({ taken, onDone }: { taken: string[]; onDone: (key: string | null) => void }) {
  const [key, setKey] = useState('')
  const k = key.trim()
  const err = k && taken.includes(k) ? `There’s already a ${k}.` : null
  return (
    <Dialog
      open
      onClose={() => onDone(null)}
      title="Add an entry"
      foot={
        <>
          <Btn variant="quiet" onClick={() => onDone(null)}>
            Cancel
          </Btn>
          <Btn variant="primary" disabled={!k || !!err} onClick={() => onDone(k)}>
            Add
          </Btn>
        </>
      }
    >
      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (k && !err) onDone(k)
        }}
      >
        <Field label="Key" error={err} hint="Usually an id like minecraft:overworld.">
          <input className="input mono" value={key} onChange={(e) => setKey(e.target.value)} autoFocus aria-label="Key" />
        </Field>
      </form>
    </Dialog>
  )
}
