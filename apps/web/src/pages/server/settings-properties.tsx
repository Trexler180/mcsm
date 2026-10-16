import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '@/lib/api'
import {
  defaultServerProperties,
  defaultServerPropertiesFor,
  parseProperties,
  propertyCategories,
  propertyFields,
  serializeProperties,
  type PropertiesMap,
  type PropertyField,
} from '@/components/servers/options/properties-schema'
import type { MyServerPermissions, Server } from '@/lib/types'
import { useGo } from '@/shell/nav'
import { paths } from '@/shell/paths'
import { Btn, Check, Empty, Search, Select, Switch, TextBtn, type StatusKey } from '@/ui/controls'
import { act } from '@/ui/failure'
import { deniedTip } from '@/ui/perm'
import { SaveBar } from '@/ui/savebar'
import { Loaded, SkForm } from '@/ui/skeleton'
import { toast } from '@/ui/toasts'
import { cap, cx, plural } from '@/ui/util'
import { allowed } from './data'

// server.properties as settings: every known key with what it does, grouped,
// filterable, and saved together. The server reads the file when it starts,
// so changes apply after a restart. The MOTD lives under General.

const DEFAULTS: PropertiesMap = parseProperties(defaultServerProperties)

export function PropertiesSettings({ server, status, perms }: { server: Server; status: StatusKey; perms: MyServerPermissions | undefined }) {
  const file = useQuery({ queryKey: ['file-content', server.id, '/server.properties'], queryFn: () => api.files.readContent(server.id, '/server.properties'), retry: false })
  const missing = file.error instanceof ApiError && file.error.status === 404
  if (missing) return <NoFile server={server} perms={perms} />
  return (
    <Loaded query={file} sk={<SkForm sections={3} />} what="server.properties">
      {(text) => <Editor server={server} status={status} perms={perms} text={text} />}
    </Loaded>
  )
}

function NoFile({ server, perms }: { server: Server; perms: MyServerPermissions | undefined }) {
  const qc = useQueryClient()
  const create = () =>
    void act('create server.properties', () => api.files.writeContent(server.id, '/server.properties', defaultServerPropertiesFor(server.port))).then((r) => {
      if (r === undefined) return
      toast('ok', 'Created server.properties')
      void qc.invalidateQueries({ queryKey: ['file-content', server.id, '/server.properties'] })
    })
  return (
    <Empty
      title="There’s no server.properties yet."
      action={
        <Btn need="files.write" disabled={!allowed(perms, 'files.write')} onClick={create}>
          Create one
        </Btn>
      }
    >
      The server writes one the first time it starts. You can create it now with the usual defaults.
    </Empty>
  )
}

function Editor({ server, status, perms, text }: { server: Server; status: StatusKey; perms: MyServerPermissions | undefined; text: string }) {
  const qc = useQueryClient()
  const go = useGo()
  const source = useMemo(() => parseProperties(text), [text])
  const [d, setD] = useState<PropertiesMap>(source)
  const [q, setQ] = useState('')
  const [nonDefaultOnly, setNonDefaultOnly] = useState(false)
  useEffect(() => setD(source), [source])
  const canEdit = allowed(perms, 'files.write')
  const live = status === 'online' || status === 'starting'
  const changed = Object.keys(d).filter((k) => d[k] !== source[k])
  const nonDefault = (k: string) => DEFAULTS[k] !== undefined && (d[k] ?? '') !== DEFAULTS[k]

  // Known keys first, then anything else the file has, under Other.
  const known = new Set(propertyFields.map((f) => f.key))
  const extra: PropertyField[] = Object.keys(source)
    .filter((k) => !known.has(k))
    .sort()
    .map((k) => ({ key: k, label: k, type: 'text', category: 'Other' }))
  const fields = [...propertyFields, ...extra].filter(
    (f) => f.key !== 'motd' && (!q || `${f.label} ${f.key}`.toLowerCase().includes(q.toLowerCase())) && (!nonDefaultOnly || nonDefault(f.key)),
  )
  const cats = propertyCategories.filter((c) => fields.some((f) => f.category === c))

  const invalid = fields.filter((f) => f.type === 'number' && d[f.key] != null && d[f.key] !== '' && !numberOk(f, d[f.key]))
  const save = async () => {
    if (invalid.length) throw new Error('Some numbers are out of range.')
    await api.files.writeContent(server.id, '/server.properties', serializeProperties(text, d))
    toast('ok', `Saved ${plural(changed.length, 'change')}`, live ? 'They take effect after a restart.' : undefined, {
      actions: live ? [{ label: 'Restart now', run: () => void act(`restart ${server.name}`, () => api.servers.restart(server.id)) }] : undefined,
    })
    void qc.invalidateQueries({ queryKey: ['file-content', server.id, '/server.properties'] })
    // A new port is adopted by the panel from the file.
    void qc.invalidateQueries({ queryKey: ['server', server.id] })
    void qc.invalidateQueries({ queryKey: ['server-identity', server.id] })
  }

  return (
    <div>
      <div className="toolbar">
        <Search value={q} onChange={setQ} placeholder="Filter settings" size="sm" style={{ width: 220 }} />
        <Check checked={nonDefaultOnly} onChange={setNonDefaultOnly} label="Changed from the default only" />
        <span className="grow" />
        <TextBtn quiet need="files" onClick={() => go(paths.server(server.id, 'files') + '?open=' + encodeURIComponent('/server.properties'))}>
          Edit the file itself
        </TextBtn>
      </div>
      <p className="small t3" style={{ marginBottom: 6 }}>
        The server reads this file when it starts, so changes apply after a restart. The MOTD is under General.
        {!canEdit && ` ${deniedTip('files.write')}`}
      </p>
      {cats.map((c) => (
        <div key={c} className="form-section">
          <h2>{c === 'Resource Pack' ? 'Resource pack' : c}</h2>
          {fields
            .filter((f) => f.category === c)
            .map((f) => {
              const edited = (d[f.key] ?? '') !== (source[f.key] ?? '')
              return (
                <div key={f.key} className="setting">
                  <div className="text">
                    <div className="name">
                      {sentenceCase(f.label)}
                      {edited && (
                        <span className="t3" style={{ fontWeight: 400 }}>
                          {' '}
                          (edited)
                        </span>
                      )}
                    </div>
                    <div className="desc">
                      <span className="mono">{f.key}</span>
                      {f.description && <>. {f.description}</>}
                      {!edited && nonDefault(f.key) && <> Default is {DEFAULTS[f.key] || 'empty'}.</>}
                    </div>
                  </div>
                  <FieldInput f={f} value={d[f.key] ?? ''} disabled={!canEdit} onChange={(v) => setD((p) => ({ ...p, [f.key]: v }))} />
                </div>
              )
            })}
        </div>
      ))}
      {cats.length === 0 && <Empty title="No settings match." />}
      <SaveBar dirty={changed.length > 0} count={changed.length} onSave={save} onReset={() => setD(source)} label={invalid.length ? 'Some numbers are out of range.' : undefined} />
    </div>
  )
}

/** "World Folder" → "World folder", leaving acronyms (IP, MOTD, RCON) alone. */
export function sentenceCase(label: string): string {
  return label.replace(/ ([A-Z])([a-z])/g, (_, a: string, b: string) => ' ' + a.toLowerCase() + b)
}

function numberOk(f: PropertyField, v: string): boolean {
  const n = Number(v)
  return !Number.isNaN(n) && (f.min == null || n >= f.min) && (f.max == null || n <= f.max)
}

function FieldInput({ f, value, disabled, onChange }: { f: PropertyField; value: string; disabled: boolean; onChange: (v: string) => void }) {
  if (f.type === 'boolean') return <Switch checked={value === 'true'} label={f.label} disabled={disabled} onChange={(x) => onChange(String(x))} />
  if (f.type === 'select' && f.options) {
    const options = f.options.includes(value) || value === '' ? f.options : [value, ...f.options]
    return (
      <Select
        size="sm"
        value={value}
        onChange={onChange}
        options={options.map((o) => ({ value: o, label: o === '' ? 'Default' : cap(o.replace('minecraft:', '').replace(/_/g, ' ')) }))}
        style={{ width: 200 }}
        disabled={disabled}
        aria-label={f.label}
      />
    )
  }
  if (f.type === 'number') {
    const bad = value !== '' && !numberOk(f, value)
    return (
      <input
        className="input sm num"
        inputMode="numeric"
        value={value}
        onChange={(e) => onChange(e.target.value.replace(/[^\d.-]/g, ''))}
        style={{ width: 130 }}
        aria-invalid={bad}
        aria-label={f.label}
        disabled={disabled}
        data-tip={bad ? `Between ${f.min ?? '…'} and ${f.max ?? '…'}` : undefined}
      />
    )
  }
  return (
    <input
      className={cx('input sm', /password|secret|rcon/.test(f.key) && 'mono')}
      type={/password/.test(f.key) ? 'password' : 'text'}
      value={value}
      placeholder={f.placeholder}
      onChange={(e) => onChange(e.target.value)}
      style={{ width: 260 }}
      aria-label={f.label}
      disabled={disabled}
    />
  )
}
