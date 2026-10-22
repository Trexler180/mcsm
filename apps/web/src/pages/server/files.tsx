import { Fragment, useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '@/lib/api'
import { parseNbt, serializeNbt, type NbtRoot } from '@/lib/nbt'
import type { FileEntry, Server } from '@/lib/types'
import { useGo } from '@/shell/nav'
import { paths } from '@/shell/paths'
import { Btn, Check, Empty, Field, IconBtn, Meter, TextBtn, type StatusKey } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtBytes, relTime } from '@/ui/format'
import { Icon } from '@/ui/icon'
import { Dialog, MenuButton, MenuItem, MenuSep } from '@/ui/overlays'
import { useCan } from '@/ui/perm'
import { FilesSkeleton, Loaded } from '@/ui/skeleton'
import { ask } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { cx, plural } from '@/ui/util'
import { CodeEditor } from './code-editor'
import { isLiveStatus } from './data'
import { NbtTree } from './nbt-view'

// The server's folder: browse, upload, rename and delete, and open a file
// beside the list to edit it (text in an editor, .dat files as a tree). The
// folder and the open file live in the address, so they survive a reload and
// the back button walks back through them.

const TEXT = /\.(properties|json|json5|mcmeta|txt|ya?ml|toml|cfg|conf|ini|log|sh|bat|cmd|ps1|md|csv|xml|lang|snbt|mcfunction|js|env|secret|list|gitignore)$/i
const NBT = /\.(dat|dat_old|nbt|schem|schematic|litematic)$/i
const MAX_TEXT = 4 * 1024 * 1024
const MAX_NBT = 16 * 1024 * 1024

export type Openable = 'text' | 'nbt' | null

/** How a file can be opened here, if at all. */
export function openable(e: Pick<FileEntry, 'name' | 'type' | 'size'>): Openable {
  if (e.type !== 'file') return null
  if (NBT.test(e.name)) return e.size <= MAX_NBT ? 'nbt' : null
  // Text by extension, or a small file with none (eula, LICENSE).
  if (TEXT.test(e.name) || (!e.name.includes('.') && e.size < 256 * 1024)) return e.size <= MAX_TEXT ? 'text' : null
  return null
}

export function join(dir: string, name: string): string {
  return (dir === '/' ? '' : dir.replace(/\/$/, '')) + '/' + name
}

export function parentOf(p: string): string {
  const i = p.replace(/\/$/, '').lastIndexOf('/')
  return i <= 0 ? '/' : p.slice(0, i)
}

/** A name a file or folder can have. */
export function entryNameError(name: string, taken: string[]): string | null {
  const n = name.trim()
  if (!n) return 'Give it a name.'
  if (n === '.' || n === '..' || /[/\\]/.test(n)) return 'A name can’t contain / or \\.'
  if (taken.some((t) => t.toLowerCase() === n.toLowerCase())) return 'Something here already has that name.'
  return null
}

export function FilesTab({
  server,
  status,
  path,
  open,
  setLocation,
}: {
  server: Server
  status: StatusKey
  path: string
  open: string | undefined
  setLocation: (path: string, open: string | undefined) => void
}) {
  const [dirty, setDirty] = useState(false)
  // Leaving a file with unsaved changes asks first.
  const leave = async (to: () => void) => {
    if (dirty && !(await ask({ title: 'Discard your changes?', body: `${open?.split('/').pop()} has changes that aren’t saved.`, confirm: 'Discard', danger: true }))) return
    setDirty(false)
    to()
  }
  return (
    <div className={cx('files-page', open && 'has-file')}>
      <Browser
        server={server}
        status={status}
        path={path}
        open={open}
        onFolder={(p) => void leave(() => setLocation(p, undefined))}
        onOpen={(p) => (p === open ? undefined : void leave(() => setLocation(path, p)))}
      />
      {open && (
        <FilePane
          key={open}
          server={server}
          status={status}
          path={open}
          onDirty={setDirty}
          onClose={() => void leave(() => setLocation(path, undefined))}
        />
      )}
    </div>
  )
}

// ---- the list -----------------------------------------------------------------------------------

function Browser({
  server,
  status,
  path,
  open,
  onFolder,
  onOpen,
}: {
  server: Server
  status: StatusKey
  path: string
  open: string | undefined
  onFolder: (p: string) => void
  onOpen: (p: string) => void
}) {
  const qc = useQueryClient()
  const can = useCan()
  const listing = useQuery({ queryKey: ['files', server.id, path], queryFn: () => api.files.list(server.id, path) })
  // Folder sizes come from a walk of everything below, which can take a moment.
  const sizes = useQuery({
    queryKey: ['file-tree', server.id, path, 1, 'sizes'],
    queryFn: () => api.files.tree(server.id, path, { depth: 1, max: 1, sizes: true }),
    staleTime: 60_000,
    retry: false,
  }).data?.sizes
  const [sel, setSel] = useState<string[]>([])
  const [over, setOver] = useState(false)
  const [up, setUp] = useState<{ loaded: number; total: number; count: number } | null>(null)
  const [naming, setNaming] = useState<{ kind: 'file' | 'dir' | 'rename'; from?: string } | null>(null)
  const pick = useRef<HTMLInputElement>(null)
  useEffect(() => setSel([]), [path])

  const entries = (listing.data?.entries ?? []).slice().sort((a, b) => (a.type === b.type ? a.name.localeCompare(b.name) : a.type === 'dir' ? -1 : 1))
  const crumbs = path.split('/').filter(Boolean)
  const rootName = server.directory_path?.split(/[\\/]/).filter(Boolean).pop() || 'server'
  const live = isLiveStatus(status)
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['files', server.id] })
    void qc.invalidateQueries({ queryKey: ['file-tree', server.id] })
  }

  const upload = async (files: File[]) => {
    if (!files.length) return
    const loaded = files.map(() => 0)
    const total = files.reduce((n, f) => n + f.size, 0)
    setUp({ loaded: 0, total, count: files.length })
    const results = await Promise.all(
      files.map((f, i) =>
        act(`upload ${f.name}`, () =>
          api.files.upload(server.id, path, f, (p) => {
            loaded[i] = p.loaded
            setUp((u) => u && { ...u, loaded: loaded.reduce((a, b) => a + b, 0) })
          }),
        ),
      ),
    )
    setUp(null)
    const ok = results.filter((r) => r !== undefined).length
    if (ok) toast('ok', ok === 1 ? `Uploaded ${files[results.findIndex((r) => r !== undefined)].name}` : `Uploaded ${plural(ok, 'file')}`)
    refresh()
  }

  const remove = async (names: string[]) => {
    const what = names.length === 1 ? names[0] : plural(names.length, 'item')
    const ok = await ask({
      title: `Delete ${what}?`,
      body: `${live ? `${server.name} is running, and deleting something it uses can crash it. ` : ''}Your last backup still has ${names.length === 1 ? 'it' : 'them'}, if there is one.`,
      confirm: 'Delete',
      danger: true,
    })
    if (!ok) return
    let done = 0
    for (const n of names) {
      const r = await act(`delete ${n}`, () => api.files.delete(server.id, join(path, n)).then(() => true))
      if (r) done++
    }
    setSel([])
    if (done) toast('ok', done === 1 && names.length === 1 ? `Deleted ${names[0]}` : `Deleted ${plural(done, 'item')}`)
    if (open && names.some((n) => open === join(path, n) || open.startsWith(join(path, n) + '/'))) onFolder(path)
    refresh()
  }

  const name = async (value: string) => {
    if (!naming) return
    const target = join(path, value.trim())
    const r = await act(naming.kind === 'rename' ? `rename ${naming.from}` : `create ${value.trim()}`, async () => {
      if (naming.kind === 'dir') await api.files.mkdir(server.id, target)
      else if (naming.kind === 'file') await api.files.writeContent(server.id, target, '')
      else await api.files.rename(server.id, join(path, naming.from as string), target)
      return true
    })
    if (!r) return
    setNaming(null)
    refresh()
    if (naming.kind === 'file') onOpen(target)
    if (naming.kind === 'rename') toast('ok', `Renamed to ${value.trim()}`)
  }

  const activate = (e: FileEntry) => {
    const full = join(path, e.name)
    if (e.type === 'dir') return onFolder(full)
    if (openable(e)) return onOpen(full)
    toast('info', `${e.name} can’t be opened here`, e.size > MAX_TEXT && TEXT.test(e.name) ? 'It’s too big to edit in the browser. Download it instead.' : 'Download it to look inside.')
  }

  const narrow = !!open
  return (
    <div
      className="files-list"
      onDragOver={(e) => {
        if (!can('files.write') || !e.dataTransfer.types.includes('Files')) return
        e.preventDefault()
        setOver(true)
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node)) setOver(false)
      }}
      onDrop={(e) => {
        e.preventDefault()
        setOver(false)
        if (can('files.write')) void upload(Array.from(e.dataTransfer.files))
      }}
    >
      <div className="files-bar">
        <nav className="crumbs grow" aria-label="Folder">
          <TextBtn quiet onClick={() => onFolder('/')}>
            {rootName}
          </TextBtn>
          {crumbs.map((c, i) => (
            <Fragment key={i}>
              <span className="t3">/</span>
              {i === crumbs.length - 1 ? <span>{c}</span> : <TextBtn quiet onClick={() => onFolder('/' + crumbs.slice(0, i + 1).join('/'))}>{c}</TextBtn>}
            </Fragment>
          ))}
        </nav>
        {sel.length > 0 ? (
          <>
            <span className="t3 small">{sel.length} selected</span>
            {sel.length === 1 && (
              <Btn size="sm" need="files.read" onClick={() => void act(`download ${sel[0]}`, () => api.files.download(server.id, join(path, sel[0])))}>
                Download
              </Btn>
            )}
            <Btn size="sm" variant="danger" need="files.delete" onClick={() => void remove(sel)}>
              Delete
            </Btn>
          </>
        ) : (
          <>
            <input
              ref={pick}
              type="file"
              multiple
              hidden
              onChange={(e) => {
                const files = Array.from(e.target.files ?? [])
                e.target.value = ''
                void upload(files)
              }}
            />
            <IconBtn icon="refresh" label="Refresh" onClick={refresh} />
            <MenuButton
              label="New"
              icon="plus"
              items={(close) => (
                <>
                  <MenuItem close={close} need="files.write" onClick={() => setNaming({ kind: 'file' })}>
                    New file
                  </MenuItem>
                  <MenuItem close={close} need="files.write" onClick={() => setNaming({ kind: 'dir' })}>
                    New folder
                  </MenuItem>
                </>
              )}
            />
            <Btn size="sm" need="files.write" loading={!!up} onClick={() => pick.current?.click()}>
              Upload
            </Btn>
          </>
        )}
      </div>
      {up && (
        <div className="files-upload">
          <Meter value={up.loaded} max={up.total || 1} label="Upload progress" />
          <span className="t3 small num">
            {plural(up.count, 'file')}, {fmtBytes(up.loaded)} of {fmtBytes(up.total)}
          </span>
        </div>
      )}
      <div className={cx('files-scroll', over && 'drop-over')}>
        <Loaded query={listing} sk={<FilesSkeleton />} what={path === '/' ? 'the server’s files' : path}>
          {() => (
            <>
              <table className={cx('t files-t', sel.length > 0 && 'selecting')}>
                {!narrow && (
                  <thead>
                    <tr>
                      <th className="pick" style={{ width: 28 }}>
                        <Check checked={sel.length > 0 && sel.length === entries.length} indeterminate={sel.length > 0 && sel.length < entries.length} onChange={(v) => setSel(v ? entries.map((e) => e.name) : [])} label="Select all" hideLabel />
                      </th>
                      <th>Name</th>
                      <th className="r" style={{ width: 100 }}>
                        Size
                      </th>
                      <th className="hide-sm" style={{ width: 120 }}>
                        Changed
                      </th>
                      <th style={{ width: 48 }} />
                    </tr>
                  </thead>
                )}
                <tbody>
                  {path !== '/' && (
                    <tr className="click" onClick={() => onFolder(parentOf(path))}>
                      {!narrow && <td />}
                      <td className="t2" colSpan={4}>
                        <span className="row" style={{ gap: 8 }}>
                          <Icon name="arrow-u" size={15} className="t3" />
                          Up a folder
                        </span>
                      </td>
                    </tr>
                  )}
                  {entries.map((e) => {
                    const full = join(path, e.name)
                    const size = e.type === 'dir' ? sizes?.[e.name] : e.size
                    return (
                      <tr key={e.name} className={cx('click', open === full && 'current')} onClick={() => activate(e)}>
                        {!narrow && (
                          <td className="pick" onClick={(ev) => ev.stopPropagation()}>
                            <Check checked={sel.includes(e.name)} onChange={(v) => setSel(v ? [...sel, e.name] : sel.filter((x) => x !== e.name))} label={`Select ${e.name}`} hideLabel />
                          </td>
                        )}
                        <td>
                          <span className="row" style={{ gap: 8 }}>
                            <Icon name={e.type === 'dir' ? 'folder' : NBT.test(e.name) ? 'file-code' : 'file'} size={15} className="t3" />
                            <span className={cx('ellipsis', e.name.endsWith('.disabled') && 't3')}>{e.name}</span>
                          </span>
                        </td>
                        <td className="r num t2 nowrap">{size != null ? fmtBytes(size) : ''}</td>
                        {!narrow && <td className="t3 hide-sm nowrap">{relTime(Date.parse(e.modified))}</td>}
                        {!narrow && (
                          <td className="actions" onClick={(ev) => ev.stopPropagation()}>
                            <MenuButton
                              label={`More for ${e.name}`}
                              items={(close) => (
                                <>
                                  {openable(e) && (
                                    <MenuItem close={close} onClick={() => onOpen(full)}>
                                      {openable(e) === 'nbt' ? 'Open' : 'Edit'}
                                    </MenuItem>
                                  )}
                                  <MenuItem close={close} need="files.read" onClick={() => void act(`download ${e.name}`, () => api.files.download(server.id, full))}>
                                    {e.type === 'dir' ? 'Download as a zip' : 'Download'}
                                  </MenuItem>
                                  <MenuItem close={close} need="files.write" onClick={() => setNaming({ kind: 'rename', from: e.name })}>
                                    Rename
                                  </MenuItem>
                                  <MenuSep />
                                  <MenuItem danger close={close} need="files.delete" onClick={() => void remove([e.name])}>
                                    Delete
                                  </MenuItem>
                                </>
                              )}
                            />
                          </td>
                        )}
                      </tr>
                    )
                  })}
                </tbody>
              </table>
              {entries.length === 0 && <Empty title="This folder is empty.">{can('files.write') ? 'Drop files here to upload them.' : undefined}</Empty>}
              {!narrow && entries.length > 0 && (
                <p className="t3 small" style={{ marginTop: 12 }}>
                  {plural(entries.length, 'item')}
                  {sizes ? `, ${fmtBytes(Object.values(sizes).reduce((a, b) => a + b, 0))}` : ''}.{can('files.write') ? ' Drop files anywhere here to upload them.' : ''}
                </p>
              )}
            </>
          )}
        </Loaded>
      </div>
      {naming && (
        <NameDialog
          title={naming.kind === 'rename' ? `Rename ${naming.from}` : naming.kind === 'dir' ? 'New folder' : 'New file'}
          initial={naming.from ?? ''}
          confirm={naming.kind === 'rename' ? 'Rename' : 'Create'}
          taken={entries.map((e) => e.name).filter((n) => n !== naming.from)}
          onClose={() => setNaming(null)}
          onSubmit={name}
        />
      )}
    </div>
  )
}

function NameDialog({ title, initial, confirm, taken, onClose, onSubmit }: { title: string; initial: string; confirm: string; taken: string[]; onClose: () => void; onSubmit: (v: string) => Promise<void> }) {
  const [v, setV] = useState(initial)
  const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState(false)
  const err = entryNameError(v, taken)
  const submit = async () => {
    setTouched(true)
    if (err || v.trim() === initial) return
    setBusy(true)
    await onSubmit(v)
    setBusy(false)
  }
  return (
    <Dialog
      open
      onClose={onClose}
      title={title}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" loading={busy} disabled={!!err || v.trim() === initial} onClick={() => void submit()}>
            {confirm}
          </Btn>
        </>
      }
    >
      <form
        onSubmit={(e) => {
          e.preventDefault()
          void submit()
        }}
      >
        <Field label="Name" error={touched || v ? (v.trim() !== initial ? err : null) : null}>
          <input
            className="input mono"
            value={v}
            autoFocus
            aria-label="Name"
            onChange={(e) => setV(e.target.value)}
            onFocus={(e) => {
              // Select the name without its extension, as file managers do.
              const dot = e.target.value.lastIndexOf('.')
              e.target.setSelectionRange(0, dot > 0 ? dot : e.target.value.length)
            }}
          />
        </Field>
      </form>
    </Dialog>
  )
}

// ---- an open file -------------------------------------------------------------------------------

function FilePane({ server, status, path, onDirty, onClose }: { server: Server; status: StatusKey; path: string; onDirty: (d: boolean) => void; onClose: () => void }) {
  const name = path.split('/').pop() ?? path
  const kind = NBT.test(name) ? 'nbt' : 'text'
  return kind === 'nbt' ? (
    <NbtPane server={server} path={path} name={name} onDirty={onDirty} onClose={onClose} />
  ) : (
    <TextPane server={server} status={status} path={path} name={name} onDirty={onDirty} onClose={onClose} />
  )
}

function PaneHead({ path, edited, children, onClose }: { path: string; edited: boolean; children?: React.ReactNode; onClose: () => void }) {
  return (
    <div className="file-head">
      <IconBtn icon="arrow-l" label="Back to the folder" className="show-sm" onClick={onClose} />
      <span className="mono small ellipsis grow">
        {path}
        {edited && <span className="t3"> (edited)</span>}
      </span>
      {children}
      <IconBtn icon="x" label="Close" className="hide-sm" onClick={onClose} />
    </div>
  )
}

function TextPane({ server, status, path, name, onDirty, onClose }: { server: Server; status: StatusKey; path: string; name: string; onDirty: (d: boolean) => void; onClose: () => void }) {
  const qc = useQueryClient()
  const go = useGo()
  const can = useCan()
  const file = useQuery({ queryKey: ['file-content', server.id, path], queryFn: () => api.files.readContent(server.id, path), retry: false, staleTime: Infinity })
  const [text, setText] = useState<string | null>(null)
  const [rev, setRev] = useState(0)
  const [saving, setSaving] = useState(false)
  const original = file.data ?? ''
  const dirty = text !== null && text !== original
  useEffect(() => onDirty(dirty), [dirty, onDirty])
  const props = path === '/server.properties'

  const save = async () => {
    if (!dirty || saving || !can('files.write')) return
    const content = text as string
    setSaving(true)
    const r = await act(`save ${name}`, () => api.files.writeContent(server.id, path, content).then(() => true))
    setSaving(false)
    if (!r) return
    qc.setQueryData(['file-content', server.id, path], content)
    setText(null)
    toast('ok', `Saved ${name}`, props && isLiveStatus(status) ? 'The server reads it when it starts, so restart to apply it.' : undefined)
    if (props) {
      void qc.invalidateQueries({ queryKey: ['server', server.id] })
      void qc.invalidateQueries({ queryKey: ['server-identity', server.id] })
    }
  }

  return (
    <div className="file-pane">
      <PaneHead path={path} edited={dirty} onClose={onClose}>
        {props && (
          <TextBtn quiet onClick={() => go(paths.server(server.id, 'settings') + '?sub=properties')}>
            Use the form
          </TextBtn>
        )}
        {dirty && (
          <Btn
            size="sm"
            variant="quiet"
            onClick={() => {
              setText(null)
              setRev((n) => n + 1)
            }}
          >
            Discard
          </Btn>
        )}
        <Btn size="sm" variant="primary" need="files.write" disabled={!dirty} loading={saving} tip="Ctrl+S" onClick={() => void save()}>
          Save
        </Btn>
      </PaneHead>
      {file.isLoading ? (
        <div className="file-body">
          <FilesSkeleton />
        </div>
      ) : file.isError ? (
        <div className="file-body">
          <Empty title={file.error instanceof ApiError && file.error.status === 404 ? `${name} isn’t there any more.` : `Couldn’t open ${name}.`}>
            {!(file.error instanceof ApiError && file.error.status === 404) && <Btn onClick={() => void file.refetch()}>Try again</Btn>}
          </Empty>
        </div>
      ) : (
        <CodeEditor key={rev} path={path} doc={original} readOnly={!can('files.write')} onChange={setText} onSave={() => void save()} />
      )}
    </div>
  )
}

function NbtPane({ server, path, name, onDirty, onClose }: { server: Server; path: string; name: string; onDirty: (d: boolean) => void; onClose: () => void }) {
  const can = useCan()
  const [rev, setRev] = useState(0)
  const file = useQuery({
    queryKey: ['file-nbt', server.id, path, rev],
    queryFn: async () => parseNbt(await api.files.viewBytes(server.id, path)),
    retry: false,
    gcTime: 0,
  })
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  useEffect(() => onDirty(dirty), [dirty, onDirty])
  const save = async (root: NbtRoot, gzipped: boolean) => {
    setSaving(true)
    const r = await act(`save ${name}`, async () => {
      await api.files.writeBytes(server.id, path, await serializeNbt(root, gzipped))
      return true
    })
    setSaving(false)
    if (r) {
      setDirty(false)
      toast('ok', `Saved ${name}`)
    }
  }
  return (
    <div className="file-pane">
      <PaneHead path={path} edited={dirty} onClose={onClose}>
        {file.data && <span className="t3 small">NBT{file.data.gzipped ? ', compressed' : ''}</span>}
        {dirty && (
          <Btn
            size="sm"
            variant="quiet"
            onClick={() => {
              setDirty(false)
              setRev((n) => n + 1)
            }}
          >
            Discard
          </Btn>
        )}
        <Btn size="sm" variant="primary" need="files.write" disabled={!dirty || !file.data} loading={saving} onClick={() => file.data && void save(file.data.root, file.data.gzipped)}>
          Save
        </Btn>
      </PaneHead>
      <div className="file-body nbt-body">
        {file.isLoading ? (
          <FilesSkeleton />
        ) : file.isError || !file.data ? (
          <Empty title={`${name} isn’t an NBT file this can read.`}>Download it to look inside.</Empty>
        ) : (
          <NbtTree root={file.data.root} readOnly={!can('files.write')} onChange={() => setDirty(true)} />
        )}
      </div>
    </div>
  )
}
