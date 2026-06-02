import { createRoute } from '@tanstack/react-router'
import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Users, Trash2 } from 'lucide-react'
import { Route as rootRoute } from './__root'
import { Header } from '@/components/layout/header'
import { Button } from '@/components/ui/button'
import { Dialog, ConfirmDialog } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from '@/components/ui/table'
import { api } from '@/lib/api'
import { useNotifications } from '@/store/notifications'
import { useAuthStore } from '@/store/auth'
import type { User } from '@/lib/types'

function CreateUserDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const qc = useQueryClient()
  const { success, error } = useNotifications()
  const [form, setForm] = useState({ email: '', password: '', role: 'user' })

  const mutation = useMutation({
    mutationFn: () => api.users.create(form.email, form.password, form.role),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['users'] })
      success('User created')
      onClose()
      setForm({ email: '', password: '', role: 'user' })
    },
    onError: (e: Error) => error('Failed to create user', e.message),
  })

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }))

  return (
    <Dialog open={open} onClose={onClose} title="Create User" className="max-w-md">
      <div className="space-y-4">
        <div className="space-y-1.5">
          <Label>Email</Label>
          <Input type="email" placeholder="user@example.com" value={form.email} onChange={f('email')} />
        </div>
        <div className="space-y-1.5">
          <Label>Password</Label>
          <Input type="password" placeholder="••••••••" value={form.password} onChange={f('password')} />
        </div>
        <div className="space-y-1.5">
          <Label>Role</Label>
          <select
            className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
            value={form.role}
            onChange={f('role')}
          >
            <option value="user">User</option>
            <option value="operator">Operator</option>
            <option value="admin">Admin</option>
          </select>
        </div>
        <div className="flex justify-end gap-3 pt-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => mutation.mutate()} loading={mutation.isPending}>
            Create
          </Button>
        </div>
      </div>
    </Dialog>
  )
}

function UsersPage() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null)
  const qc = useQueryClient()
  const { success, error } = useNotifications()
  const { user: currentUser } = useAuthStore()

  const { data: users = [], isLoading } = useQuery({
    queryKey: ['users'],
    queryFn: api.users.list,
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.users.delete(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['users'] })
      success('User deleted')
      setDeleteTarget(null)
    },
    onError: (e: Error) => error('Delete failed', e.message),
  })

  const roleBadge = (role: User['role']) => {
    const styles: Record<User['role'], string> = {
      admin: 'bg-red-500/15 text-red-400 border-red-500/30',
      operator: 'bg-yellow-500/15 text-yellow-400 border-yellow-500/30',
      user: 'bg-surface-2 text-text-secondary border-border',
    }
    return (
      <span className={`text-xs px-2 py-0.5 rounded border ${styles[role]} capitalize`}>
        {role}
      </span>
    )
  }

  return (
    <div>
      <Header
        title="Users"
        description={`${users.length} user${users.length !== 1 ? 's' : ''}`}
        actions={
          <Button onClick={() => setShowCreate(true)} size="sm">
            <Plus className="h-4 w-4" /> New User
          </Button>
        }
      />
      <div className="p-6">
        {isLoading ? (
          <div className="flex justify-center py-16">
            <div className="w-6 h-6 border-2 border-accent border-t-transparent rounded-full animate-spin" />
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Email</TableHead>
                <TableHead>Display Name</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>Last Login</TableHead>
                <TableHead>Joined</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.map((u) => (
                <TableRow key={u.id}>
                  <TableCell className="font-medium">{u.email}</TableCell>
                  <TableCell className="text-text-secondary">{u.display_name ?? '—'}</TableCell>
                  <TableCell>{roleBadge(u.role)}</TableCell>
                  <TableCell className="text-text-secondary text-sm">
                    {u.last_login ? new Date(u.last_login).toLocaleString() : '—'}
                  </TableCell>
                  <TableCell className="text-text-secondary text-sm">
                    {new Date(u.created_at).toLocaleDateString()}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => setDeleteTarget(u)}
                      disabled={u.id === currentUser?.id}
                      title={u.id === currentUser?.id ? 'Cannot delete yourself' : 'Delete user'}
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

      <CreateUserDialog open={showCreate} onClose={() => setShowCreate(false)} />

      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        title="Delete user"
        description={`Delete "${deleteTarget?.email}"? This cannot be undone.`}
        confirmLabel="Delete"
        variant="destructive"
        loading={deleteMutation.isPending}
      />
    </div>
  )
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/users',
  component: UsersPage,
})
