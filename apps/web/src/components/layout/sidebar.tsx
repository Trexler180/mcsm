import { Link, useRouterState } from '@tanstack/react-router'
import { Server, Layers, Users, LayoutDashboard, LogOut } from 'lucide-react'
import { clsx } from 'clsx'
import { useAuthStore } from '@/store/auth'

const nav = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard, exact: true },
  { to: '/servers', label: 'Servers', icon: Server },
  { to: '/nodes', label: 'Nodes', icon: Layers },
  { to: '/users', label: 'Users', icon: Users, adminOnly: true },
]

export function Sidebar() {
  const { user, logout } = useAuthStore()
  const router = useRouterState()
  const currentPath = router.location.pathname

  return (
    <aside className="flex flex-col w-56 border-r border-border bg-surface h-screen sticky top-0">
      {/* Logo */}
      <div className="flex items-center gap-2.5 px-5 h-14 border-b border-border">
        <div className="w-7 h-7 rounded bg-accent flex items-center justify-center flex-shrink-0">
          <Server className="h-4 w-4 text-black" />
        </div>
        <span className="font-bold text-text-primary tracking-tight">MCSM</span>
      </div>

      {/* Navigation */}
      <nav className="flex-1 overflow-y-auto p-3 space-y-0.5">
        {nav.map(({ to, label, icon: Icon, exact, adminOnly }) => {
          if (adminOnly && user?.role !== 'admin') return null
          const active = exact ? currentPath === to : currentPath.startsWith(to) && to !== '/'
          return (
            <Link
              key={to}
              to={to}
              className={clsx(
                'flex items-center gap-3 px-3 py-2 rounded-md text-sm transition-colors',
                active
                  ? 'bg-accent/10 text-accent'
                  : 'text-text-secondary hover:text-text-primary hover:bg-surface-2',
              )}
            >
              <Icon className="h-4 w-4 flex-shrink-0" />
              {label}
            </Link>
          )
        })}
      </nav>

      {/* User info */}
      <div className="border-t border-border p-3">
        <div className="flex items-center gap-3 px-3 py-2 mb-1">
          <div className="w-7 h-7 rounded-full bg-accent/20 flex items-center justify-center flex-shrink-0">
            <span className="text-xs font-bold text-accent">
              {user?.email?.[0]?.toUpperCase() ?? '?'}
            </span>
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-xs font-medium text-text-primary truncate">{user?.email}</p>
            <p className="text-xs text-text-secondary capitalize">{user?.role}</p>
          </div>
        </div>
        <button
          onClick={logout}
          className="flex items-center gap-3 px-3 py-2 w-full text-sm text-text-secondary hover:text-text-primary hover:bg-surface-2 rounded-md transition-colors"
        >
          <LogOut className="h-4 w-4" />
          Sign out
        </button>
      </div>
    </aside>
  )
}
