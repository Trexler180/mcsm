import { createRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Layers, Trash2, CheckCircle2, XCircle } from 'lucide-react'
import { Route as rootRoute } from './__root'
import { Header } from '@/components/layout/header'
import { Button } from '@/components/ui/button'
import { Dialog, ConfirmDialog } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from '@/components/ui/table'
import { api } from '@/lib/api'
import { useNotifications } from '@/store/notifications'
import type { Node } from '@/lib/types'

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
          <div className="flex items-center justify-between gap-3 rounded-md border border-border bg-surface-2 px-3 py-2 text-sm">
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

function NodesPage() {
  const [showCreate, setShowCreate] = useState(false)
  const [editTarget, setEditTarget] = useState<Node | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Node | null>(null)
  const qc = useQueryClient()
  const { success, error } = useNotifications()

  const { data: nodes = [], isLoading } = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api.nodes.list(),
    refetchInterval: 15_000,
  })

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
          <div className="text-center py-16 text-text-secondary">
            <Layers className="h-10 w-10 mx-auto mb-3 opacity-30" />
            <p>No nodes yet</p>
            <Button className="mt-4" onClick={() => setShowCreate(true)}>
              Add your first node
            </Button>
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Host</TableHead>
                <TableHead>Port</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Last Seen</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {nodes.map((node) => (
                <TableRow
                  key={node.id}
                  className="cursor-pointer"
                  onClick={() => setEditTarget(node)}
                >
                  <TableCell className="font-medium">{node.name}</TableCell>
                  <TableCell className="font-mono text-sm">{node.fqdn}</TableCell>
                  <TableCell>{node.port}</TableCell>
                  <TableCell>
                    {node.online ? (
                      <span className="flex items-center gap-1.5 text-green-400 text-sm">
                        <CheckCircle2 className="h-3.5 w-3.5" /> Online
                      </span>
                    ) : (
                      <span className="flex items-center gap-1.5 text-text-secondary text-sm">
                        <XCircle className="h-3.5 w-3.5" /> Offline
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-text-secondary text-sm">
                    {node.last_seen ? new Date(node.last_seen).toLocaleString() : '—'}
                  </TableCell>
                  <TableCell
                    className="text-right"
                    onClick={(e) => e.stopPropagation()}
                  >
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => setDeleteTarget(node)}
                      title="Remove node"
                    >
                      <Trash2 className="h-3.5 w-3.5 text-red-400" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>

      <NodeDialog open={showCreate} onClose={() => setShowCreate(false)} />

      <NodeDialog
        open={editTarget !== null}
        node={editTarget}
        onClose={() => setEditTarget(null)}
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

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/nodes',
  component: NodesPage,
})
