import { createRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Users, Trash2 } from 'lucide-react'
import { Route as rootRoute } from './__root'
import { Header } from '@/components/layout/header'
import { Button } from '@/components/ui/button'
import { Dialog, ConfirmDialog } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { EmptyState } from '@/components/ui/empty-state'
import { api } from '@/lib/api'
import { relativeTime } from '@/lib/time'
import { useNotifications } from '@/store/notifications'
import { useAuthStore } from '@/store/auth'
import type { User } from '@/lib/types'

const EMPTY_USER_FORM = { email: '', display_name: '', password: '', role: 'user' }

const ROLE_STYLES: Record<User['role'], string> = {
  admin: 'bg-red-500/15 text-red-400 border-red-500/30',
  operator: 'bg-yellow-500/15 text-yellow-400 border-yellow-500/30',
  user: 'bg-surface-2 text-text-secondary border-border',
}

function roleBadge(role: User['role']) {
  return (
    <span className={`text-xs px-2 py-0.5 rounded border ${ROLE_STYLES[role]} capitalize`}>
      {role}
    </span>
  )
}

function initials(user: User): string {
  const src = user.display_name || user.email || '?'
  const parts = src.trim().split(/[\s@._-]+/).filter(Boolean)
  const letters = parts.length >= 2 ? parts[0][0] + parts[1][0] : src.slice(0, 2)
  return letters.toUpperCase()
}

function UserCard({
  user,
  isSelf,
  onEdit,
  onDelete,
}: {
  user: User
  isSelf: boolean
  onEdit: () => void
  onDelete: () => void
}) {
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
      <div className="flex items-center gap-3">
        <div
          className={`flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-full border text-sm font-semibold ${ROLE_STYLES[user.role]}`}
        >
          {initials(user)}
        </div>
        <div className="min-w-0 flex-1">
          <p className="flex items-center gap-1.5 truncate font-medium text-text-primary">
            {user.display_name || user.email}
            {isSelf && (
              <span className="rounded bg-accent/15 px-1.5 py-0.5 text-[10px] font-medium text-accent">
                You
              </span>
            )}
          </p>
          {user.display_name && (
            <p className="truncate text-xs text-text-secondary">{user.email}</p>
          )}
        </div>
        <div className="flex flex-shrink-0 items-center gap-1.5">
          {roleBadge(user.role)}
          {!isSelf && (
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation()
                onDelete()
              }}
              title="Delete user"
              className="rounded-md p-1 text-text-secondary opacity-0 transition-opacity hover:bg-surface-2 hover:text-red-400 focus:opacity-100 group-hover:opacity-100"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      </div>

      <div className="flex items-center justify-between gap-2 border-t border-border/50 pt-2 text-xs text-text-secondary">
        <span>
          {user.last_login
            ? `Active ${relativeTime(user.last_login)}`
            : 'Never signed in'}
        </span>
        <span>Joined {new Date(user.created_at).toLocaleDateString()}</span>
      </div>
    </div>
  )
}

function UserDialog({
  open,
  onClose,
  user,
}: {
  open: boolean
  onClose: () => void
  user?: User | null
}) {
  const qc = useQueryClient()
  const { success, error } = useNotifications()
  const isEdit = !!user
  const [form, setForm] = useState(EMPTY_USER_FORM)

  // Load the user being edited (or reset) whenever the dialog opens.
  useEffect(() => {
    if (!open) return
    setForm(
      user
        ? {
            email: user.email,
            display_name: user.display_name ?? '',
            password: '',
            role: user.role,
          }
        : EMPTY_USER_FORM,
    )
  }, [open, user])

  const mutation = useMutation({
    mutationFn: () =>
      isEdit
        ? api.users.update(user!.id, {
            display_name: form.display_name,
            role: form.role,
            ...(form.password ? { password: form.password } : {}),
          })
        : api.users.create(form.email, form.password, form.role),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['users'] })
      success(isEdit ? 'User updated' : 'User created')
      onClose()
    },
    onError: (e: Error) =>
      error(isEdit ? 'Update failed' : 'Failed to create user', e.message),
  })

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }))

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={isEdit ? 'Edit User' : 'Create User'}
      className="max-w-md"
    >
      <div className="space-y-4">
        <div className="space-y-1.5">
          <Label>Email</Label>
          <Input
            type="email"
            placeholder="user@example.com"
            value={form.email}
            onChange={f('email')}
            disabled={isEdit}
          />
        </div>
        {isEdit && (
          <div className="space-y-1.5">
            <Label>Display Name</Label>
            <Input
              placeholder="Optional"
              value={form.display_name}
              onChange={f('display_name')}
            />
          </div>
        )}
        <div className="space-y-1.5">
          <Label>{isEdit ? 'Reset Password' : 'Password'}</Label>
          <Input
            type="password"
            placeholder={isEdit ? 'Leave blank to keep current' : '••••••••'}
            value={form.password}
            onChange={f('password')}
          />
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
          <p className="text-xs text-text-secondary">
            Operator is reserved and does not grant server access by itself.
            Manage server access from each server&apos;s Access tab.
          </p>
        </div>
        <div className="flex justify-end gap-3 pt-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => mutation.mutate()} loading={mutation.isPending}>
            {isEdit ? 'Save' : 'Create'}
          </Button>
        </div>
      </div>
    </Dialog>
  )
}

function UsersPage() {
  const [showCreate, setShowCreate] = useState(false)
  const [editTarget, setEditTarget] = useState<User | null>(null)
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
      <div className="p-4 sm:p-6">
        {isLoading ? (
          <div className="flex justify-center py-16">
            <div className="w-6 h-6 border-2 border-accent border-t-transparent rounded-full animate-spin" />
          </div>
        ) : users.length === 0 ? (
          <EmptyState
            icon={Users}
            title="No users yet"
            hint="Invite teammates to manage servers with you."
            action={
              <Button onClick={() => setShowCreate(true)}>
                <Plus className="h-4 w-4" /> New User
              </Button>
            }
          />
        ) : (
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
            {users.map((u) => (
              <UserCard
                key={u.id}
                user={u}
                isSelf={u.id === currentUser?.id}
                onEdit={() => setEditTarget(u)}
                onDelete={() => setDeleteTarget(u)}
              />
            ))}
          </div>
        )}
      </div>

      <UserDialog open={showCreate} onClose={() => setShowCreate(false)} />

      <UserDialog
        open={editTarget !== null}
        user={editTarget}
        onClose={() => setEditTarget(null)}
      />

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
