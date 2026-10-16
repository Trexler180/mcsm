import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { defaultServerProperties, parseProperties, serializeProperties } from '@/pages/server/properties-schema'
import type { FleetServer, MyServerPermissions, Server } from '@/lib/types'
import { useAuthStore } from '@/store/auth'
import { useFolders } from '@/shell/fleet'
import { useGo } from '@/shell/nav'
import { paths } from '@/shell/paths'
import { BLOCKS, BLOCK_KINDS, type BlockKind } from '@/ui/blocks'
import { Btn, Check, Choices, Copy, Field, Select, Switch, TextBtn, type StatusKey } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtBytes, fmtMB } from '@/ui/format'
import { Dialog } from '@/ui/overlays'
import { deniedTip } from '@/ui/perm'
import { BlockSwatch, MC_COLORS, MC_COLOR_NAMES, blockIconPng, imageIconPng, stripMotd } from '@/ui/pixel'
import { SaveBar } from '@/ui/savebar'
import { ask } from '@/ui/store'
import { toast } from '@/ui/toasts'
import { allowed, useIdentity } from './data'
import { MultiplayerEntry } from './multiplayer'
import { platformLabel } from '@/ui/platforms'

// General settings: how the server looks in the multiplayer list, what it's
// called, where it lives, how it runs, its resource pack, and copying or
// deleting it. Everything but the icon (which applies at once) is saved
// together from the bar at the bottom.

const ICON = '/server-icon.png'
const PACK_DIR = '/resource-packs'
const SLUG_RE = /^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$/

interface Pack {
  enabled: boolean
  path: string
  public_id: string
  sha1: string
  file_name: string
  source: 'uploaded' | 'world'
  required: boolean
  prompt: string
}

interface Draft {
  name: string
  description: string
  folder_id: string
  directory_path: string
  port: string
  ram_mb_min: string
  ram_mb_max: string
  auto_start: boolean
  public_status: boolean
  public_slug: string
  connect_address: string
  helper_mod: boolean
  motd: string
  pack: Pack
}

function newId() {
  const c = globalThis.crypto as Crypto & { randomUUID?: () => string }
  if (c.randomUUID) return c.randomUUID()
  const b = new Uint8Array(16)
  c.getRandomValues(b)
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}

function packOf(server: Server): Pack {
  const s = (server.settings?.resource_pack ?? {}) as Partial<Pack>
  return {
    enabled: s.enabled ?? false,
    path: s.path ?? '',
    public_id: s.public_id ?? newId(),
    sha1: s.sha1 ?? '',
    file_name: s.file_name ?? '',
    source: s.source ?? (s.path?.toLowerCase().endsWith('/resources.zip') ? 'world' : 'uploaded'),
    required: s.required ?? false,
    prompt: s.prompt ?? '',
  }
}

async function sha1Of(bytes: ArrayBuffer): Promise<string> {
  const d = await crypto.subtle.digest('SHA-1', bytes)
  return Array.from(new Uint8Array(d), (b) => b.toString(16).padStart(2, '0')).join('')
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)

/** A value as Java writes it in a properties file: backslashes doubled, the
 *  line break as \n, anything outside printable ASCII (the § codes) as
 *  \uXXXX, which every server version reads the same. */
export function encodePropValue(v: string): string {
  return v
    .replace(/\\/g, '\\\\')
    .replace(/\r?\n/g, '\\n')
    .replace(/[^\x20-\x7e]/g, (c) => '\\u' + c.charCodeAt(0).toString(16).toUpperCase().padStart(4, '0'))
}

export function GeneralSettings({ server, live, status, perms }: { server: Server; live: FleetServer | undefined; status: StatusKey; perms: MyServerPermissions | undefined }) {
  const qc = useQueryClient()
  const go = useGo()
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  const folders = useFolders().data ?? []
  const identity = useIdentity(server.id).data
  const helper = useQuery({ queryKey: ['server', server.id, 'helper-mod'], queryFn: () => api.servers.helperMod(server.id), retry: false }).data
  const view = useQuery({ queryKey: ['server-settings', server.id], queryFn: () => api.servers.settings(server.id), retry: false }).data
  const canFiles = allowed(perms, 'files.write')
  const up = status === 'online'

  const source = useMemo<Draft>(
    () => ({
      name: server.name,
      description: server.description ?? '',
      folder_id: server.folder_id ?? '',
      directory_path: server.directory_path ?? '',
      port: String(server.port),
      ram_mb_min: String(server.ram_mb_min),
      ram_mb_max: String(server.ram_mb_max),
      auto_start: server.auto_start,
      public_status: server.public_status ?? false,
      public_slug: server.public_slug ?? '',
      connect_address: view?.values.connect_address ?? '',
      helper_mod: helper?.enabled ?? false,
      motd: identity?.motd ?? '',
      pack: packOf(server),
    }),
    [server, view, helper, identity],
  )
  const [d, setD] = useState<Draft>(source)
  // A fresh copy from the server replaces the draft only while nothing is edited.
  const [baseline, setBaseline] = useState<Draft>(source)
  useEffect(() => {
    setD((cur) => (same(cur, baseline) ? source : cur))
    setBaseline(source)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [source])
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD((p) => ({ ...p, [k]: v }))
  const setPack = (patch: Partial<Pack>) => setD((p) => ({ ...p, pack: { ...p.pack, ...patch } }))
  const changed = (Object.keys(d) as (keyof Draft)[]).filter((k) => !same(d[k], source[k]))
  const dirty = changed.length > 0

  const lines = d.motd.split('\n')
  const motdTooLong = lines.length > 2 || lines.some((l) => stripMotd(l).length > 45)
  const slugBad = d.public_status && !!d.public_slug && !SLUG_RE.test(d.public_slug)
  const portN = Number(d.port)
  const ramMin = Number(d.ram_mb_min)
  const ramMax = Number(d.ram_mb_max)
  const numbersOk = Number.isInteger(portN) && portN >= 1 && portN <= 65535 && Number.isInteger(ramMax) && ramMax >= 256 && Number.isInteger(ramMin) && ramMin >= 128 && ramMin <= ramMax
  const valid = !!d.name.trim() && !slugBad && numbersOk && (!d.pack.enabled || !!d.pack.path)

  const save = async () => {
    if (!valid) throw new Error('Some fields need fixing first.')
    const has = (k: keyof Draft) => changed.includes(k)
    const serverFields: (keyof Draft)[] = ['name', 'description', 'folder_id', 'directory_path', 'port', 'ram_mb_min', 'ram_mb_max', 'auto_start', 'public_status', 'public_slug']
    let pack = d.pack
    if (serverFields.some(has)) {
      await api.servers.update(server.id, {
        name: d.name.trim(),
        description: d.description.trim() || null,
        folder_id: d.folder_id || null,
        ...(server.directory_path !== null && has('directory_path') ? { directory_path: d.directory_path.trim() } : {}),
        port: portN,
        ram_mb_min: ramMin,
        ram_mb_max: ramMax,
        auto_start: d.auto_start,
        public_status: d.public_status,
        public_slug: d.public_status ? d.public_slug : server.public_slug,
      })
    }
    if (has('motd') || has('pack')) {
      if (has('pack') && pack.enabled && !pack.sha1) {
        pack = { ...pack, sha1: await sha1Of((await api.files.readBytes(server.id, pack.path)).slice().buffer as ArrayBuffer) }
      }
      const props = await api.files.readContent(server.id, '/server.properties').catch(() => defaultServerProperties)
      const values: Record<string, string> = { ...parseProperties(props) }
      if (has('motd')) values.motd = encodePropValue(d.motd)
      if (has('pack')) {
        values['resource-pack'] = pack.enabled ? api.resourcePacks.publicUrl(server.id, pack.public_id) : ''
        values['resource-pack-id'] = ''
        values['resource-pack-sha1'] = pack.enabled ? pack.sha1 : ''
        values['require-resource-pack'] = String(pack.enabled && pack.required)
        values['resource-pack-prompt'] = pack.enabled ? encodePropValue(pack.prompt) : ''
      }
      await api.files.writeContent(server.id, '/server.properties', serializeProperties(props, values))
      if (has('pack')) {
        await api.servers.update(server.id, { settings: { ...server.settings, resource_pack: { ...pack, updated_at: new Date().toISOString() } } })
      }
    }
    if (has('helper_mod')) await api.servers.setHelperMod(server.id, d.helper_mod)
    if (has('connect_address')) await api.servers.patchSettings(server.id, { connect_address: d.connect_address.trim() })
    const restartNote = (has('motd') || has('pack') || has('port') || has('helper_mod') || has('ram_mb_max') || has('ram_mb_min')) && up
    toast('ok', 'Saved', restartNote ? 'Some of this applies after a restart.' : undefined)
    for (const key of [['server', server.id], ['servers'], ['fleet'], ['server-identity', server.id], ['server-settings', server.id], ['server', server.id, 'helper-mod'], ['file-content', server.id, '/server.properties']]) {
      void qc.invalidateQueries({ queryKey: key })
    }
  }

  return (
    <div>
      <MultiplayerSection server={server} live={live} draft={d} identityIcon={identity?.icon} up={up} canFiles={canFiles} onMotd={(v) => set('motd', v)} motdTooLong={motdTooLong} />

      <div className="form-section">
        <h2>Name and description</h2>
        <div className="form-grid" style={{ marginTop: 12 }}>
          <Field label="Name" error={!d.name.trim() ? 'It needs a name.' : null}>
            <input className="input" value={d.name} onChange={(e) => set('name', e.target.value)} />
          </Field>
          <Field label="Folder in the sidebar">
            <Select
              value={d.folder_id}
              onChange={(v) => set('folder_id', v)}
              disabled={!admin}
              options={[{ value: '', label: 'None' }, ...folders.map((f) => ({ value: f.id, label: f.name }))]}
              aria-label="Folder"
            />
          </Field>
          <Field label="Description" className="span" hint="For you and other admins. Players don’t see it.">
            <input className="input" value={d.description} onChange={(e) => set('description', e.target.value)} />
          </Field>
        </div>
      </div>

      <div className="form-section">
        <h2>Address and memory</h2>
        <div className="form-grid" style={{ marginTop: 12 }}>
          <Field
            label="Address players use"
            hint={view?.values.connect_address ? undefined : identity?.address ? `Leave empty to show ${identity.address}, from the node and port.` : 'Leave empty to use the node’s address and the port.'}
          >
            <input className="input mono" value={d.connect_address} onChange={(e) => set('connect_address', e.target.value)} placeholder={identity?.address_source === 'node' ? identity.address : 'play.example.net'} />
          </Field>
          <Field label="Port" error={!(portN >= 1 && portN <= 65535) ? 'A port is 1 to 65535.' : null} hint="Written to server.properties.">
            <input className="input num" inputMode="numeric" value={d.port} onChange={(e) => set('port', e.target.value.replace(/\D/g, ''))} />
          </Field>
          <Field label="Memory" hint={`At most ${fmtMB(ramMax || 0)} for the Java heap.`} error={!(ramMax >= 256) ? 'At least 256 MB.' : null}>
            <MemoryInput value={d.ram_mb_max} onChange={(v) => set('ram_mb_max', v)} />
          </Field>
          <Field label="Memory at start" error={!(ramMin >= 128 && ramMin <= ramMax) ? 'Between 128 MB and the memory above.' : null}>
            <MemoryInput value={d.ram_mb_min} onChange={(v) => set('ram_mb_min', v)} />
          </Field>
          {server.directory_path !== null && (
            <Field label="Folder on the node" className="span" hint="Only administrators see this. Changing it doesn’t move any files.">
              <input className="input mono" value={d.directory_path} onChange={(e) => set('directory_path', e.target.value)} />
            </Field>
          )}
        </div>
      </div>

      <div className="form-section">
        <h2>Running</h2>
        <div className="setting">
          <div className="text">
            <div className="name">Start when its node starts</div>
            <div className="desc">After a reboot or a power cut.</div>
          </div>
          <Switch checked={d.auto_start} onChange={(v) => set('auto_start', v)} label="Start automatically" />
        </div>
        <div className="setting">
          <div className="text">
            <div className="name">MCSM helper mod</div>
            <div className="desc">
              {helper?.supported === false
                ? `It doesn’t run on ${platformLabel(server.platform)} servers.`
                : 'Adds tick timings, entity counts and dependable player actions. Installed and removed on the next start.'}
            </div>
          </div>
          <Switch checked={d.helper_mod} onChange={(v) => set('helper_mod', v)} disabled={helper?.supported === false} label="Helper mod" />
        </div>
        <div className="setting">
          <div className="text">
            <div className="name">Public status page</div>
            <div className="desc">A page anyone can see with whether it’s up and how many people are on. Nothing else.</div>
          </div>
          <Switch
            checked={d.public_status}
            onChange={(v) => setD((p) => ({ ...p, public_status: v, public_slug: p.public_slug || p.name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 63) }))}
            label="Public status page"
          />
        </div>
        {d.public_status && (
          <Field error={slugBad ? 'Lowercase letters, numbers and dashes, 3 to 63 of them.' : null}>
            <div className="row" style={{ gap: 10 }}>
              <div className="affix" style={{ maxWidth: 420, flex: 1 }}>
                <span>/status/</span>
                <input className="input" value={d.public_slug} onChange={(e) => set('public_slug', e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ''))} aria-label="Status page address" />
              </div>
              {server.public_status && server.public_slug && !slugBad && <Copy text={`${window.location.origin}/status/${server.public_slug}`}>Copy the link</Copy>}
            </div>
          </Field>
        )}
      </div>

      <PackSection server={server} pack={d.pack} setPack={setPack} canFiles={canFiles} />

      <DangerSection server={server} admin={admin} perms={perms} onDeleted={() => go(paths.home)} />

      <SaveBar dirty={dirty} count={changed.length} onSave={save} onReset={() => setD(source)} label={!valid && dirty ? 'Some fields need fixing before this can be saved.' : undefined} />
    </div>
  )
}

/** Memory in MB, typed in GB or MB. */
function MemoryInput({ value, onChange }: { value: string; onChange: (mb: string) => void }) {
  const mb = Number(value) || 0
  const [unit, setUnit] = useState<'GB' | 'MB'>(mb && mb % 1024 === 0 ? 'GB' : 'MB')
  const shown = unit === 'GB' ? String(mb / 1024) : value
  return (
    <div className="row" style={{ gap: 8 }}>
      <input
        className="input num"
        inputMode="decimal"
        style={{ width: 110 }}
        value={shown}
        onChange={(e) => {
          const v = e.target.value.replace(/[^\d.]/g, '')
          onChange(unit === 'GB' ? String(Math.round(Number(v || 0) * 1024)) : v.replace(/\..*$/, ''))
        }}
      />
      <Select size="sm" value={unit} onChange={setUnit} options={['GB', 'MB']} aria-label="Unit" style={{ width: 'auto' }} />
    </div>
  )
}

// ---- in the multiplayer list ----------------------------------------------------------------

function MultiplayerSection({
  server,
  live,
  draft,
  identityIcon,
  up,
  canFiles,
  onMotd,
  motdTooLong,
}: {
  server: Server
  live: FleetServer | undefined
  draft: Draft
  identityIcon?: string
  up: boolean
  canFiles: boolean
  onMotd: (v: string) => void
  motdTooLong: boolean
}) {
  const qc = useQueryClient()
  const motdRef = useRef<HTMLTextAreaElement>(null)
  const upRef = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState(false)
  const identity = useIdentity(server.id).data
  const insert = (code: string) => {
    const el = motdRef.current
    const a = el ? el.selectionStart : draft.motd.length
    const b = el ? el.selectionEnd : draft.motd.length
    onMotd(draft.motd.slice(0, a) + '§' + code + draft.motd.slice(b))
    setTimeout(() => {
      el?.focus()
      el?.setSelectionRange(a + 2, a + 2)
    }, 0)
  }
  const setIcon = async (label: string, make: () => Promise<Uint8Array | null>) => {
    setBusy(true)
    const r = await act(label, async () => {
      const bytes = await make()
      if (bytes) await api.files.writeBytes(server.id, ICON, bytes)
      else await api.files.delete(server.id, ICON)
      return true
    })
    setBusy(false)
    if (r === undefined) return
    toast('ok', 'The icon is saved', up ? 'Players see it after a restart.' : undefined)
    void qc.invalidateQueries({ queryKey: ['server-identity', server.id] })
  }
  return (
    <div className="form-section">
      <h2>In the multiplayer list</h2>
      <p className="lede">What players see before they join. The icon and the two-line message (the MOTD) come from the server’s files.</p>
      <MultiplayerEntry id={server.id} icon={identityIcon} name={draft.name} motd={draft.motd} up={true} online={up ? (live?.players ?? 0) : 0} max={identity?.max_players} />
      <div style={{ marginTop: 16 }}>
        <Field
          label="Message"
          error={motdTooLong ? 'Minecraft shows two lines of about 45 characters. The rest gets cut off.' : null}
          hint={canFiles ? 'Colours and styles use § codes. Click one to insert it where your cursor is.' : deniedTip('files.write')}
        >
          <textarea ref={motdRef} className="textarea mono" rows={2} value={draft.motd} disabled={!canFiles} onChange={(e) => onMotd(e.target.value)} aria-label="MOTD" style={{ fontSize: 13.5 }} />
        </Field>
        {canFiles && (
          <div className="codes" style={{ marginTop: 8 }} role="group" aria-label="Formatting codes">
            {Object.keys(MC_COLORS).map((k) => (
              <button key={k} type="button" onClick={() => insert(k)} data-tip={`${MC_COLOR_NAMES[k]} (§${k})`} aria-label={MC_COLOR_NAMES[k]} style={{ background: MC_COLORS[k] }} />
            ))}
            <button type="button" onClick={() => insert('l')} data-tip="Bold (§l)" aria-label="Bold">
              B
            </button>
            <button type="button" onClick={() => insert('o')} data-tip="Italic (§o)" aria-label="Italic" style={{ fontStyle: 'italic' }}>
              I
            </button>
            <button type="button" onClick={() => insert('n')} data-tip="Underline (§n)" aria-label="Underline" style={{ textDecoration: 'underline' }}>
              U
            </button>
            <button type="button" onClick={() => insert('r')} data-tip="Reset (§r)" aria-label="Reset">
              R
            </button>
          </div>
        )}
      </div>
      <div style={{ marginTop: 18 }}>
        <div className="field-label" style={{ marginBottom: 8 }}>
          Icon
        </div>
        <div className="row wrap" style={{ gap: 6 }}>
          {BLOCK_KINDS.map((k: BlockKind) => (
            <button
              key={k}
              type="button"
              disabled={!canFiles || busy}
              onClick={() => void setIcon('save the icon', () => blockIconPng(k))}
              data-tip={BLOCKS[k].label}
              aria-label={BLOCKS[k].label}
              style={{ borderRadius: 6, padding: 2 }}
            >
              <BlockSwatch kind={k} size={26} />
            </button>
          ))}
          <input
            ref={upRef}
            type="file"
            accept="image/png,image/jpeg,image/webp,image/gif"
            hidden
            onChange={(e) => {
              const f = e.target.files?.[0]
              e.target.value = ''
              if (f) void setIcon('save the icon', () => imageIconPng(f))
            }}
          />
          <Btn size="sm" need="files.write" loading={busy} onClick={() => upRef.current?.click()}>
            Upload an image
          </Btn>
          {identityIcon && (
            <TextBtn quiet need="files.delete" onClick={() => void setIcon('remove the icon', async () => null)}>
              Remove it
            </TextBtn>
          )}
        </div>
        <p className="field-hint" style={{ marginTop: 6 }}>
          Pick a block, or upload an image: it’s cropped square and saved as a 64 by 64 server-icon.png.
        </p>
      </div>
    </div>
  )
}

// ---- resource pack ----------------------------------------------------------------------------

function PackSection({ server, pack, setPack, canFiles }: { server: Server; pack: Pack; setPack: (p: Partial<Pack>) => void; canFiles: boolean }) {
  const qc = useQueryClient()
  const fileRef = useRef<HTMLInputElement>(null)
  const [pct, setPct] = useState<number | null>(null)
  const uploaded = useQuery({ queryKey: ['files', server.id, PACK_DIR], queryFn: () => api.files.list(server.id, PACK_DIR), retry: false }).data
  const worldPacks = useQuery({
    queryKey: ['world-resource-packs', server.id],
    queryFn: async () => {
      const root = await api.files.list(server.id, '/')
      const dirs = root.entries.filter((e) => e.type === 'dir' && e.name !== PACK_DIR.slice(1))
      const found = await Promise.all(
        dirs.map(async (dir) => {
          const listing = await api.files.list(server.id, '/' + dir.name).catch(() => null)
          const f = listing?.entries.find((e) => e.type === 'file' && e.name.toLowerCase() === 'resources.zip')
          return f ? { path: `/${dir.name}/resources.zip`, world: dir.name, size: f.size } : null
        }),
      )
      return found.filter((x): x is { path: string; world: string; size: number } => !!x)
    },
    retry: false,
  }).data
  const zips = (uploaded?.entries ?? []).filter((e) => e.type === 'file' && e.name.toLowerCase().endsWith('.zip'))
  const options = [
    ...zips.map((z) => ({ value: `${PACK_DIR}/${z.name}`, label: z.name, hint: fmtBytes(z.size) })),
    ...(worldPacks ?? []).map((w) => ({ value: w.path, label: `${w.world}/resources.zip`, hint: 'from the world' })),
  ]
  const upload = async (file: File) => {
    if (!file.name.toLowerCase().endsWith('.zip')) {
      toast('bad', 'Resource packs are .zip files')
      return
    }
    const name = (file.name.split(/[\\/]/).pop() ?? 'resource-pack.zip').replace(/[^a-zA-Z0-9._-]/g, '-')
    const stored = new File([file], name, { type: file.type || 'application/zip' })
    const sha1 = await sha1Of(await file.arrayBuffer())
    setPct(0)
    const r = await act(`upload ${name}`, () => api.files.upload(server.id, PACK_DIR, stored, (p) => setPct(p.total ? Math.round((p.loaded / p.total) * 100) : 0)))
    setPct(null)
    if (r === undefined) return
    setPack({ enabled: true, path: `${PACK_DIR}/${name}`, sha1, file_name: name, source: 'uploaded' })
    void qc.invalidateQueries({ queryKey: ['files', server.id, PACK_DIR] })
    toast('ok', `Uploaded ${name}`, 'Save to send it to players.')
  }
  return (
    <div className="form-section">
      <h2>Resource pack</h2>
      <p className="lede">Sent to players when they join. The panel serves the file itself, from the server’s folder.</p>
      <Choices
        value={pack.enabled ? 'on' : 'off'}
        onChange={(v) => setPack({ enabled: v === 'on' })}
        columns={2}
        options={[
          { value: 'off', label: 'No resource pack' },
          { value: 'on', label: 'Send one', disabled: !canFiles, tip: canFiles ? undefined : deniedTip('files.write') },
        ]}
      />
      {pack.enabled && (
        <div className="stack" style={{ gap: 14, marginTop: 14 }}>
          <div className="row wrap" style={{ gap: 8, alignItems: 'flex-end' }}>
            <Field label="Which pack" className="grow">
              <Select
                value={pack.path}
                onChange={(v) => setPack({ path: v, sha1: '', file_name: v.split('/').pop() ?? '', source: v.toLowerCase().endsWith('/resources.zip') ? 'world' : 'uploaded' })}
                options={options.length ? options : [{ value: '', label: 'Upload one first', disabled: true }]}
                placeholder="Choose a pack"
                aria-label="Which pack"
              />
            </Field>
            <input
              ref={fileRef}
              type="file"
              accept=".zip,application/zip"
              hidden
              onChange={(e) => {
                const f = e.target.files?.[0]
                e.target.value = ''
                if (f) void upload(f)
              }}
            />
            <Btn need="files.write" loading={pct !== null} onClick={() => fileRef.current?.click()}>
              {pct !== null ? `Uploading ${pct}%` : 'Upload a .zip'}
            </Btn>
          </div>
          <div className="setting">
            <div className="text">
              <div className="name">Players must accept it</div>
              <div className="desc">Anyone who says no is disconnected.</div>
            </div>
            <Switch checked={pack.required} onChange={(v) => setPack({ required: v })} label="Require the pack" />
          </div>
          <Field label="What to tell them">
            <input className="input" value={pack.prompt} onChange={(e) => setPack({ prompt: e.target.value })} placeholder="Our custom items need this pack." />
          </Field>
          {pack.path && (
            <p className="small t3">
              Served at <Copy text={api.resourcePacks.publicUrl(server.id, pack.public_id)} mono>{`…/resource-pack/${pack.public_id.slice(0, 8)}`}</Copy>
              {pack.sha1 && <>, SHA-1 {pack.sha1.slice(0, 12)}…</>}
            </p>
          )}
        </div>
      )}
    </div>
  )
}

// ---- copy or delete ---------------------------------------------------------------------------

function DangerSection({ server, admin, perms, onDeleted }: { server: Server; admin: boolean; perms: MyServerPermissions | undefined; onDeleted: () => void }) {
  const [cloning, setCloning] = useState(false)
  const [deleting, setDeleting] = useState(false)
  return (
    <div className="form-section" style={{ borderBottom: 0 }}>
      <h2>Copy or delete</h2>
      <div className="setting">
        <div className="text">
          <div className="name">Make a copy</div>
          <div className="desc">Same files, mods and settings, on the same node, on another port. Handy for testing updates.</div>
        </div>
        <Btn disabled={!admin} tip={admin ? undefined : 'Only administrators can copy a server'} onClick={() => setCloning(true)}>
          Copy
        </Btn>
      </div>
      <div className="setting">
        <div className="text">
          <div className="name">Delete {server.name}</div>
          <div className="desc">It stops, and the panel forgets it. You choose whether its folder and backups go too.</div>
        </div>
        <Btn variant="danger" need="admin" disabled={!allowed(perms, 'admin')} onClick={() => setDeleting(true)}>
          Delete
        </Btn>
      </div>
      {cloning && <CloneDialog server={server} onClose={() => setCloning(false)} />}
      {deleting && <DeleteDialog server={server} onClose={() => setDeleting(false)} onDeleted={onDeleted} />}
    </div>
  )
}

function CloneDialog({ server, onClose }: { server: Server; onClose: () => void }) {
  const qc = useQueryClient()
  const go = useGo()
  const [name, setName] = useState(`${server.name} copy`)
  const [dir, setDir] = useState('')
  const [mods, setMods] = useState(true)
  const [busy, setBusy] = useState(false)
  const run = async () => {
    setBusy(true)
    const r = await act(`copy ${server.name}`, () => api.servers.clone(server.id, { name: name.trim(), directory_path: dir.trim() || undefined, copy_mods: mods }))
    setBusy(false)
    if (r === undefined) return
    const notes = [r.mods_queued > 0 ? `Installing ${r.mods_queued} mods in the background.` : '', r.mods_skipped > 0 ? `${r.mods_skipped} uploaded jars need uploading again.` : ''].filter(Boolean).join(' ')
    toast('ok', `Made ${r.server.name}`, notes || undefined)
    void qc.invalidateQueries({ queryKey: ['fleet'] })
    void qc.invalidateQueries({ queryKey: ['servers'] })
    onClose()
    go(paths.server(r.server.id))
  }
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Copy ${server.name}`}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" disabled={!name.trim()} loading={busy} onClick={() => void run()}>
            Make the copy
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 14 }}>
        <Field label="Name">
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </Field>
        <Field label="Folder on the node" hint="Leave empty to put it next to the original.">
          <input className="input mono" value={dir} onChange={(e) => setDir(e.target.value)} />
        </Field>
        <Check checked={mods} onChange={setMods} label="Install the same mods" />
        <p className="small t3">The copy gets its own port. Its worlds are a snapshot of the original’s right now.</p>
      </div>
    </Dialog>
  )
}

function DeleteDialog({ server, onClose, onDeleted }: { server: Server; onClose: () => void; onDeleted: () => void }) {
  const qc = useQueryClient()
  const [files, setFiles] = useState(false)
  const [backups, setBackups] = useState(false)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const ok = typed === server.name
  const run = async () => {
    if (!ok) return
    if (files && !(await ask({ title: 'Delete its files for good?', body: `The worlds in ${server.name}’s folder can’t be brought back${backups ? ', and neither can its backups' : ''}.`, confirm: 'Delete them', danger: true }))) return
    setBusy(true)
    const r = await act(`delete ${server.name}`, () => api.servers.delete(server.id, { files, backups }).then(() => true))
    setBusy(false)
    if (r === undefined) return
    toast('ok', `Deleted ${server.name}`)
    void qc.invalidateQueries({ queryKey: ['fleet'] })
    void qc.invalidateQueries({ queryKey: ['servers'] })
    onClose()
    onDeleted()
  }
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Delete ${server.name}?`}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="danger primary" disabled={!ok} loading={busy} onClick={() => void run()}>
            Delete
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 14 }}>
        <p className="t2">It stops, and the panel forgets it. Unless you tick the boxes, its folder and backups stay on the node.</p>
        <Check checked={files} onChange={setFiles} label="Also delete its folder: worlds, settings and mods" />
        <Check checked={backups} onChange={setBackups} label="Also delete its backups" />
        <Field
          label={
            <>
              Type <b>{server.name}</b> to confirm
            </>
          }
        >
          <input className="input" value={typed} onChange={(e) => setTyped(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && void run()} />
        </Field>
      </div>
    </Dialog>
  )
}
