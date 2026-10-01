import { useEffect, useMemo, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Plus,
  Layers,
  Trash2,
  Cpu,
  HardDrive,
  MapPin,
  MemoryStick,
  Monitor,
  Server as ServerIcon,
  Tag,
  Timer,
  CheckCircle2,
  XCircle,
  Power,
} from 'lucide-react'
import { Header } from '@/components/layout/header'
import { Button } from '@/components/ui/button'
import { Dialog, ConfirmDialog } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { EmptyState } from '@/components/ui/empty-state'
import { RebootNodeDialog } from '@/components/nodes/reboot-dialog'
import { api } from '@/lib/api'
import { relativeTime } from '@/lib/time'
import { useNotifications } from '@/store/notifications'
import type { Node } from '@/lib/types'

function formatUptime(seconds: number | null | undefined): string | null {
  if (!seconds || seconds <= 0) return null
  const d = Math.floor(seconds / 86_400)
  const h = Math.floor((seconds % 86_400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

function formatGb(gb: number): string {
  return gb >= 1024 ? `${(gb / 1024).toFixed(1)} TB` : `${gb} GB`
}

function usageColor(pct: number): string {
  if (pct >= 90) return 'bg-red-500'
  if (pct >= 75) return 'bg-amber-500'
  return 'bg-accent'
}

function UsageMeter({
  icon,
  label,
  pct,
  detail,
}: {
  icon: React.ReactNode
  label: string
  pct: number | null
  detail: string
}) {
  const clamped = pct === null ? null : Math.min(100, Math.max(0, pct))
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="flex items-center gap-1.5 text-text-secondary">
          {icon}
          {label}
        </span>
        <span className="font-mono text-text-primary">{detail}</span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-surface-2">
        {clamped !== null && (
          <div
            className={`h-full rounded-full transition-all ${usageColor(clamped)}`}
            style={{ width: `${clamped}%` }}
          />
        )}
      </div>
    </div>
  )
}

function NodeCard({
  node,
  serverCount,
  onEdit,
  onDelete,
  onReboot,
}: {
  node: Node
  serverCount: number
  onEdit: () => void
  onDelete: () => void
  onReboot: () => void
}) {
  // Usage percentages; null (no bar fill) when the agent hasn't reported yet.
  const memPct =
    node.mem_used_mb != null && node.memory_mb
      ? (node.mem_used_mb / node.memory_mb) * 100
      : null
  const diskPct =
    node.disk_used_gb != null && node.disk_gb
      ? (node.disk_used_gb / node.disk_gb) * 100
      : null
  const cpuPct = node.cpu_pct

  const memDetail =
    node.mem_used_mb != null && node.memory_mb
      ? `${(node.mem_used_mb / 1024).toFixed(1)} / ${Math.round(node.memory_mb / 1024)} GB`
      : node.memory_mb
        ? `${Math.round(node.memory_mb / 1024)} GB`
        : '—'
  const diskDetail =
    node.disk_used_gb != null && node.disk_gb
      ? `${formatGb(node.disk_used_gb)} / ${formatGb(node.disk_gb)} · ${formatGb(node.disk_gb - node.disk_used_gb)} free`
      : node.disk_gb
        ? formatGb(node.disk_gb)
        : '—'
  const cpuDetail =
    cpuPct != null
      ? `${cpuPct.toFixed(0)}%${node.cpu_cores ? ` · ${node.cpu_cores}c` : ''}`
      : node.cpu_cores
        ? `${node.cpu_cores} cores`
        : '—'

  const uptime = node.online ? formatUptime(node.uptime_seconds) : null

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onEdit}
      onKeyDown={(e) => {
        if (e.key === 'Enter') onEdit()
      }}
      className="group relative flex cursor-pointer flex-col gap-3 rounded-lg border border-border bg-surface p-4 text-left transition-colors hover:border-accent/40"
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate font-medium text-text-primary">{node.name}</p>
          <p className="truncate font-mono text-xs text-text-secondary">
            {node.scheme}://{node.fqdn}:{node.port}
          </p>
        </div>
        <div className="flex flex-shrink-0 items-center gap-1.5">
          <span
            className={`flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs ${
              node.online
                ? 'bg-green-900/40 text-green-400'
                : 'bg-surface-2 text-text-secondary'
            }`}
          >
            <span
              className={`h-1.5 w-1.5 rounded-full ${node.online ? 'bg-green-400' : 'bg-gray-500'}`}
            />
            {node.online ? 'Online' : 'Offline'}
          </span>
          {node.online && (
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation()
                onReboot()
              }}
              title="Reboot host"
              aria-label={`Reboot ${node.name}`}
              className="rounded-md p-1 text-text-secondary opacity-0 transition-opacity hover:bg-surface-2 hover:text-amber-400 focus:opacity-100 group-hover:opacity-100"
            >
              <Power className="h-3.5 w-3.5" />
            </button>
          )}
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation()
              onDelete()
            }}
            title="Remove node"
            className="rounded-md p-1 text-text-secondary opacity-0 transition-opacity hover:bg-surface-2 hover:text-red-400 focus:opacity-100 group-hover:opacity-100"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      </div>

      <div className="space-y-2.5">
        <UsageMeter
          icon={<MemoryStick className="h-3.5 w-3.5" />}
          label="Memory"
          pct={memPct}
          detail={memDetail}
        />
        <UsageMeter
          icon={<HardDrive className="h-3.5 w-3.5" />}
          label="Disk"
          pct={diskPct}
          detail={diskDetail}
        />
        <UsageMeter
          icon={<Cpu className="h-3.5 w-3.5" />}
          label="CPU"
          pct={cpuPct}
          detail={cpuDetail}
        />
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs text-text-secondary">
        <span className="flex items-center gap-1.5" title="Servers on this node">
          <ServerIcon className="h-3.5 w-3.5" />
          <span className="text-text-primary">{serverCount}</span>
          server{serverCount !== 1 ? 's' : ''}
        </span>
        {node.os && (
          <span className="flex items-center gap-1.5" title="Operating system">
            <Monitor className="h-3.5 w-3.5" />
            <span className="text-text-primary">
              {node.os}
              {node.arch ? `/${node.arch}` : ''}
            </span>
          </span>
        )}
        {uptime && (
          <span className="flex items-center gap-1.5" title="Host uptime">
            <Timer className="h-3.5 w-3.5" />
            <span className="text-text-primary">{uptime}</span>
          </span>
        )}
        {node.agent_version && (
          <span className="flex items-center gap-1.5" title="Agent version">
            <Tag className="h-3.5 w-3.5" />
            <span className="font-mono text-text-primary">
              {node.agent_version.slice(0, 10)}
            </span>
          </span>
        )}
      </div>

      <div className="flex items-center justify-between gap-2 border-t border-border/50 pt-2 text-xs text-text-secondary">
        <span className="flex items-center gap-1.5 truncate">
          <MapPin className="h-3.5 w-3.5 flex-shrink-0" />
          {node.location || 'No location'}
        </span>
        <span className="flex-shrink-0">
          {node.last_seen ? `Seen ${relativeTime(node.last_seen)}` : 'Never seen'}
        </span>
      </div>
    </div>
  )
}

const EMPTY_NODE_FORM = { name: '', fqdn: '', port: '8090', location: '', token: '' }

function NodeDialog({
  open,
  onClose,
  node,
}: {
  open: boolean
  onClose: () => void
  node?: Node | null
}) {
  const qc = useQueryClient()
  const { success, error } = useNotifications()
  const isEdit = !!node
  const [form, setForm] = useState(EMPTY_NODE_FORM)

  // Load the node being edited (or reset) whenever the dialog opens.
  useEffect(() => {
    if (!open) return
    setForm(
      node
        ? {
            name: node.name,
            fqdn: node.fqdn,
            port: String(node.port),
            location: node.location ?? '',
            token: '',
          }
        : EMPTY_NODE_FORM,
    )
  }, [open, node])

  const mutation = useMutation({
    mutationFn: () =>
      isEdit
        ? api.nodes.update(node!.id, {
            name: form.name,
            fqdn: form.fqdn,
            port: Number(form.port),
            location: form.location || null,
          })
        : api.nodes.create({
            name: form.name,
            fqdn: form.fqdn,
            port: Number(form.port),
            scheme: 'http',
            location: form.location || null,
            token: form.token,
          }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['nodes'] })
      success(isEdit ? 'Node updated' : 'Node added')
      onClose()
    },
    onError: (e: Error) =>
      error(isEdit ? 'Update failed' : 'Failed to add node', e.message),
  })

  const f =
    (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }))

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={isEdit ? 'Edit Node' : 'Add Node'}
      className="max-w-md"
    >
      <div className="space-y-4">
        {isEdit && node && (
          <div className="space-y-2 rounded-md border border-border bg-surface-2 px-3 py-2 text-sm">
            <div className="flex items-center justify-between gap-3">
              {node.online ? (
                <span className="flex items-center gap-1.5 text-green-400">
                  <CheckCircle2 className="h-3.5 w-3.5" /> Online
                </span>
              ) : (
                <span className="flex items-center gap-1.5 text-text-secondary">
                  <XCircle className="h-3.5 w-3.5" /> Offline
                </span>
              )}
              <span className="text-text-secondary">
                {node.last_seen
                  ? `Last seen ${new Date(node.last_seen).toLocaleString()}`
                  : 'Never seen'}
              </span>
            </div>
            {(node.os || node.agent_version || node.uptime_seconds) && (
              <div className="flex flex-wrap gap-x-4 gap-y-1 border-t border-border/50 pt-2 text-xs text-text-secondary">
                {node.os && (
                  <span>
                    OS{' '}
                    <span className="text-text-primary">
                      {node.os}
                      {node.arch ? `/${node.arch}` : ''}
                    </span>
                  </span>
                )}
                {node.online && formatUptime(node.uptime_seconds) && (
                  <span>
                    Up{' '}
                    <span className="text-text-primary">
                      {formatUptime(node.uptime_seconds)}
                    </span>
                  </span>
                )}
                {node.agent_version && (
                  <span>
                    Agent{' '}
                    <span className="font-mono text-text-primary">
                      {node.agent_version.slice(0, 10)}
                    </span>
                  </span>
                )}
              </div>
            )}
          </div>
        )}
        <div className="space-y-1.5">
          <Label>Name</Label>
          <Input placeholder="Node 1" value={form.name} onChange={f('name')} />
        </div>
        <div className="space-y-1.5">
          <Label>Hostname / IP</Label>
          <Input placeholder="node1.example.com" value={form.fqdn} onChange={f('fqdn')} />
        </div>
        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-1.5">
            <Label>Agent Port</Label>
            <Input type="number" value={form.port} onChange={f('port')} />
          </div>
          <div className="space-y-1.5">
            <Label>Location</Label>
            <Input placeholder="Optional" value={form.location} onChange={f('location')} />
          </div>
        </div>
        {!isEdit && (
          <div className="space-y-1.5">
            <Label>Agent Token</Label>
            <Input
              type="password"
              placeholder="Secret token from agent config"
              value={form.token}
              onChange={f('token')}
            />
          </div>
        )}
        <div className="flex justify-end gap-3 pt-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => mutation.mutate()} loading={mutation.isPending}>
            {isEdit ? 'Save' : 'Add Node'}
          </Button>
        </div>
      </div>
    </Dialog>
  )
}

export function NodesPage() {
  const [showCreate, setShowCreate] = useState(false)
  const [editTarget, setEditTarget] = useState<Node | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Node | null>(null)
  const [rebootTarget, setRebootTarget] = useState<Node | null>(null)
  const qc = useQueryClient()
  const { success, error } = useNotifications()

  const { data: nodes = [], isLoading } = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api.nodes.list(),
    refetchInterval: 15_000,
  })

  // Count servers per node from the already-cached servers list (admin-only).
  const { data: servers = [] } = useQuery({
    queryKey: ['servers'],
    queryFn: () => api.servers.list(),
  })
  const serverCounts = useMemo(() => {
    const m = new Map<string, number>()
    for (const s of servers) m.set(s.node_id, (m.get(s.node_id) ?? 0) + 1)
    return m
  }, [servers])

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.nodes.delete(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['nodes'] })
      success('Node removed')
      setDeleteTarget(null)
    },
    onError: (e: Error) => error('Delete failed', e.message),
  })

  return (
    <div>
      <Header
        title="Nodes"
        description={`${nodes.length} node${nodes.length !== 1 ? 's' : ''}`}
        actions={
          <Button onClick={() => setShowCreate(true)} size="sm">
            <Plus className="h-4 w-4" /> Add Node
          </Button>
        }
      />
      <div className="p-4 sm:p-6">
        {isLoading ? (
          <div className="flex justify-center py-16">
            <div className="w-6 h-6 border-2 border-accent border-t-transparent rounded-full animate-spin" />
          </div>
        ) : nodes.length === 0 ? (
          <EmptyState
            icon={Layers}
            title="No nodes yet"
            hint="Add a node to start hosting servers on remote agents."
            action={
              <Button onClick={() => setShowCreate(true)}>
                <Plus className="h-4 w-4" /> Add your first node
              </Button>
            }
          />
        ) : (
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
            {nodes.map((node) => (
              <NodeCard
                key={node.id}
                node={node}
                serverCount={serverCounts.get(node.id) ?? 0}
                onEdit={() => setEditTarget(node)}
                onDelete={() => setDeleteTarget(node)}
                onReboot={() => setRebootTarget(node)}
              />
            ))}
          </div>
        )}
      </div>

      <NodeDialog open={showCreate} onClose={() => setShowCreate(false)} />

      <NodeDialog
        open={editTarget !== null}
        node={editTarget}
        onClose={() => setEditTarget(null)}
      />

      <RebootNodeDialog
        node={rebootTarget}
        serverCount={rebootTarget ? (serverCounts.get(rebootTarget.id) ?? 0) : 0}
        onClose={() => setRebootTarget(null)}
      />

      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        title="Remove node"
        description={`Remove "${deleteTarget?.name}"? Servers on this node will become inaccessible.`}
        confirmLabel="Remove"
        variant="destructive"
        loading={deleteMutation.isPending}
      />
    </div>
  )
}
