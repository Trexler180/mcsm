import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { FleetServer, ImportCandidate, Node } from '@/lib/types'
import { useFleet, useFolders } from '@/shell/fleet'
import { useGo } from '@/shell/nav'
import { paths } from '@/shell/paths'
import { Btn, Choices, Field, Notice, Select } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtMB } from '@/ui/format'
import { PageHead } from '@/ui/page'
import { PLATFORMS, platformLabel } from '@/ui/platforms'
import { Loaded, SkForm } from '@/ui/skeleton'
import { toast } from '@/ui/toasts'

// Adding a server: a new one MCSM downloads and sets up, or an existing
// folder on a node that MCSM starts managing as it is.

type Mode = 'new' | 'import'

/** Memory already promised to a node's servers. */
export function promisedOn(nodeId: string, servers: Pick<FleetServer, 'node_id' | 'ram_limit_mb'>[]): number {
  return servers.filter((s) => s.node_id === nodeId).reduce((n, s) => n + (s.ram_limit_mb || 0), 0)
}

/** The first port from 25565 up that no server on the node uses. */
export function freePort(nodeId: string, servers: Pick<FleetServer, 'node_id' | 'port'>[]): number {
  const used = new Set(servers.filter((s) => s.node_id === nodeId).map((s) => s.port))
  let p = 25565
  while (used.has(p)) p++
  return p
}

/** "my-old-smp" → "My old smp", for a name picked from a folder. */
export function nameFromFolder(dir: string): string {
  const base = dir.split(/[\\/]/).filter(Boolean).pop() ?? dir
  const words = base.replace(/[-_]+/g, ' ').trim()
  return words.charAt(0).toUpperCase() + words.slice(1)
}

const MIN_MB = 1024
const STEP_MB = 512
// Kept back for the node itself and the agent.
const RESERVE_MB = 1024

export function NewServerPage() {
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: ({ signal }) => api.nodes.list(signal) })
  return (
    <div className="scroll">
      <div className="page narrow">
        <PageHead title="Add a server" />
        <Loaded query={nodes} sk={<SkForm sections={2} />} what="your nodes">
          {(list) => (list.length ? <NewServerForm nodes={list} /> : <Notice tone="warn">Add a node first: servers run on one.</Notice>)}
        </Loaded>
      </div>
    </div>
  )
}

function NewServerForm({ nodes }: { nodes: Node[] }) {
  const qc = useQueryClient()
  const go = useGo()
  const fleet = useFleet().data
  const folders = useFolders().data ?? []
  const servers = fleet?.servers ?? []
  const firstOnline = nodes.find((n) => n.online) ?? nodes[0]
  const [mode, setMode] = useState<Mode>('new')
  const [nodeId, setNodeId] = useState(firstOnline.id)
  const [dir, setDir] = useState('')
  const [f, setF] = useState({ name: '', platform: 'paper', mc_version: '', port: String(freePort(firstOnline.id, servers)), ram: 4096, folder: '' })
  const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState(false)
  const node = nodes.find((n) => n.id === nodeId)
  const versions = useQuery({ queryKey: ['mc-versions', f.platform, false], queryFn: () => api.minecraft.versions(f.platform, false), staleTime: 30 * 60_000 }).data
  const candidates = useQuery({ queryKey: ['import-candidates', nodeId], queryFn: () => api.servers.importCandidates(nodeId), enabled: mode === 'import' && !!node?.online })
  const cand = candidates.data?.find((c) => c.directory === dir)

  // The newest stable version until one is picked (or detected).
  useEffect(() => {
    if (!f.mc_version && versions?.length) setF((p) => ({ ...p, mc_version: versions.find((v) => v.stable)?.version ?? versions[0].version }))
  }, [versions, f.mc_version])

  const pickNode = (id: string) => {
    setNodeId(id)
    setDir('')
    setF((p) => ({ ...p, port: String(freePort(id, servers)) }))
  }
  const pickFolder = (d: string) => {
    setDir(d)
    const c = candidates.data?.find((x) => x.directory === d)
    if (c) setF((p) => ({ ...p, name: p.name || nameFromFolder(c.directory), platform: c.platform || p.platform, mc_version: c.mc_version || p.mc_version, port: c.port ? String(c.port) : p.port }))
  }

  const promised = node ? promisedOn(node.id, servers) : 0
  const total = node?.memory_mb ?? 16384
  const left = Math.max(0, total - promised - RESERVE_MB)
  const ceiling = Math.max(MIN_MB + STEP_MB, Math.min(total, 32768))
  const port = Number(f.port)
  const clash = servers.find((s) => s.node_id === nodeId && s.port === port)
  const errors = {
    name: f.name.trim() ? null : 'Give it a name.',
    port: !Number.isInteger(port) || port < 1 || port > 65535 ? 'A port is 1 to 65535.' : clash ? `${clash.name} already uses ${port} on ${node?.name}.` : null,
    node: !node?.online ? `${node?.name ?? 'That node'} is offline.` : null,
    folder: mode === 'import' && !dir ? 'Choose the folder to add.' : null,
    version: f.mc_version ? null : 'Choose a version.',
  }
  const over = f.ram > left

  const create = async () => {
    setTouched(true)
    if (Object.values(errors).some(Boolean)) return
    setBusy(true)
    const r = await act(mode === 'import' ? `add ${f.name.trim()}` : `create ${f.name.trim()}`, () =>
      api.servers.create({
        name: f.name.trim(),
        node_id: nodeId,
        platform: f.platform,
        mc_version: f.mc_version,
        port,
        ram_mb_max: f.ram,
        folder_id: f.folder || null,
        ...(mode === 'import' && cand ? { import_existing: true, directory_path: cand.directory, jar_file: cand.jar_file } : {}),
      }),
    )
    setBusy(false)
    if (!r) return
    void qc.invalidateQueries({ queryKey: ['fleet'] })
    void qc.invalidateQueries({ queryKey: ['servers'] })
    void qc.invalidateQueries({ queryKey: ['server-folders'] })
    toast('ok', mode === 'import' ? `Added ${r.name}` : `Created ${r.name}`, mode === 'new' ? 'It downloads what it needs the first time it starts.' : undefined, {
      actions: [{ label: 'Start it', run: () => void act(`start ${r.name}`, () => api.servers.start(r.id)) }],
    })
    go(paths.server(r.id))
  }
  const err = (k: keyof typeof errors) => (touched ? errors[k] : null)

  return (
    <div className="stack" style={{ gap: 22 }}>
      <Choices
        value={mode}
        onChange={(v) => {
          setMode(v)
          setDir('')
        }}
        columns={2}
        options={[
          { value: 'new', label: 'New server', desc: 'MCSM downloads the server software and accepts the EULA for you.' },
          { value: 'import', label: 'An existing folder', desc: 'A server already on the node. Nothing on disk is changed.' },
        ]}
      />
      <Field label="Node" error={err('node')}>
        <Select
          value={nodeId}
          onChange={pickNode}
          aria-label="Node"
          options={nodes.map((n) => ({ value: n.id, label: n.online ? `${n.name} (${fmtMB(Math.max(0, (n.memory_mb ?? 0) - promisedOn(n.id, servers)))} not promised)` : `${n.name} (offline)`, disabled: !n.online }))}
        />
      </Field>
      {mode === 'import' && (
        <Field label="Folder" error={err('folder')} hint={candidates.data && !candidates.data.length ? `There are no server folders on ${node?.name} that MCSM doesn’t manage already.` : 'Server folders on the node that MCSM doesn’t manage yet.'}>
          <Select
            value={dir}
            onChange={pickFolder}
            aria-label="Folder"
            disabled={candidates.isLoading}
            options={[{ value: '', label: candidates.isLoading ? 'Looking…' : 'Choose a folder' }, ...(candidates.data ?? []).map((c) => ({ value: c.directory, label: candidateLabel(c) }))]}
          />
          {cand && !cand.eula_accepted && <p className="small warn" style={{ marginTop: 8 }}>Its EULA isn’t accepted yet; MCSM accepts it when the server first starts.</p>}
        </Field>
      )}
      <div className="form-grid">
        <Field label="Name" className="span" error={err('name')}>
          <input className="input" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder="Survival" autoFocus aria-label="Name" />
        </Field>
        <Field label="Software">
          <Select
            value={f.platform}
            onChange={(v) => setF({ ...f, platform: v, mc_version: '' })}
            aria-label="Software"
            options={Object.keys(PLATFORMS).map((k) => ({ value: k, label: platformLabel(k) + (PLATFORMS[k].content === 'mod' ? ', for mods' : PLATFORMS[k].content === 'plugin' ? ', for plugins' : '') }))}
          />
        </Field>
        <Field label="Minecraft version" error={err('version')}>
          <Select
            value={f.mc_version}
            onChange={(v) => setF({ ...f, mc_version: v })}
            aria-label="Minecraft version"
            options={(versions ?? [])
              .filter((v) => v.stable || v.version === f.mc_version)
              .slice(0, 60)
              .map((v) => ({ value: v.version, label: v.version }))
              .concat(f.mc_version && !versions?.some((v) => v.version === f.mc_version) ? [{ value: f.mc_version, label: f.mc_version }] : [])}
          />
        </Field>
        <Field label="Port" error={err('port')}>
          <input className="input num" inputMode="numeric" value={f.port} onChange={(e) => setF({ ...f, port: e.target.value.replace(/\D/g, '') })} aria-label="Port" />
        </Field>
        <Field label="Folder in the sidebar">
          <Select value={f.folder} onChange={(v) => setF({ ...f, folder: v })} aria-label="Folder in the sidebar" options={[{ value: '', label: 'None' }, ...folders.map((x) => ({ value: x.id, label: x.name }))]} />
        </Field>
      </div>
      <Field
        label="Memory"
        aside={<span className="num" style={{ color: 'var(--text)' }}>{fmtMB(f.ram)}</span>}
        error={over && node ? `${node.name} only has ${fmtMB(left)} that isn’t promised to other servers. It can still run, but they may run short.` : null}
        hint={node ? `${fmtMB(promised)} of ${node.name}’s ${fmtMB(total)} is promised to other servers.` : undefined}
      >
        <input
          type="range"
          className="range"
          min={MIN_MB}
          max={ceiling}
          step={STEP_MB}
          value={Math.min(f.ram, ceiling)}
          onChange={(e) => setF({ ...f, ram: Number(e.target.value) })}
          aria-label="Memory"
          style={{ ['--fill' as string]: `${((Math.min(f.ram, ceiling) - MIN_MB) / (ceiling - MIN_MB)) * 100}%` }}
        />
      </Field>
      <div className="row" style={{ gap: 8 }}>
        <Btn variant="primary" loading={busy} onClick={() => void create()}>
          {mode === 'import' ? 'Add server' : 'Create server'}
        </Btn>
        <Btn variant="quiet" onClick={() => history.back()}>
          Cancel
        </Btn>
      </div>
    </div>
  )
}

function candidateLabel(c: ImportCandidate): string {
  const bits = [c.platform && c.mc_version ? `${platformLabel(c.platform)} ${c.mc_version}` : c.platform ? platformLabel(c.platform) : '', c.has_world ? 'has a world' : '', c.mod_count ? `${c.mod_count} mods` : '', c.plugin_count ? `${c.plugin_count} plugins` : ''].filter(Boolean)
  return bits.length ? `${c.directory} (${bits.join(', ')})` : c.directory
}
