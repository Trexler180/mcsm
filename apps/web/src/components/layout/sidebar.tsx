import { Link, useRouterState } from '@tanstack/react-router'
import {
  Server,
  Layers,
  Users,
  LayoutDashboard,
  LogOut,
  ScrollText,
  Settings,
  X,
} from 'lucide-react'
import { clsx } from 'clsx'
import { useAuthStore } from '@/store/auth'
import { useUiStore } from '@/store/ui'

const nav = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard, exact: true },
  { to: '/servers', label: 'Servers', icon: Server },
  { to: '/nodes', label: 'Nodes', icon: Layers },
  { to: '/users', label: 'Users', icon: Users, adminOnly: true },
  { to: '/audit', label: 'Audit Log', icon: ScrollText, adminOnly: true },
  { to: '/settings', label: 'Settings', icon: Settings, adminOnly: true },
]

// Shared inner content for both the desktop sidebar and the mobile drawer.
function SidebarContent({ onNavigate }: { onNavigate?: () => void }) {
  const { user, logout } = useAuthStore()
  const router = useRouterState()
  const currentPath = router.location.pathname

  return (
    <>
      {/* Logo */}
      <div className="flex items-center gap-2.5 px-5 h-14 border-b border-border flex-shrink-0">
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
              onClick={onNavigate}
              className={clsx(
                'flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors',
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

      {/* User info — links to the account page (security + sessions). */}
      <div className="border-t border-border p-3 flex-shrink-0">
        <Link
          to="/account"
          onClick={onNavigate}
          className={clsx(
            'flex items-center gap-3 px-3 py-2 mb-1 rounded-md transition-colors',
            currentPath.startsWith('/account')
              ? 'bg-accent/10'
              : 'hover:bg-surface-2',
          )}
        >
          <div className="w-7 h-7 rounded-full bg-accent/20 flex items-center justify-center flex-shrink-0">
            <span className="text-xs font-bold text-accent">
              {user?.email?.[0]?.toUpperCase() ?? '?'}
            </span>
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-xs font-medium text-text-primary truncate">{user?.email}</p>
            <p className="text-xs text-text-secondary capitalize">{user?.role}</p>
          </div>
        </Link>
        <button
          onClick={() => {
            logout()
            onNavigate?.()
          }}
          className="flex items-center gap-3 px-3 py-2.5 w-full text-sm text-text-secondary hover:text-text-primary hover:bg-surface-2 rounded-md transition-colors"
        >
          <LogOut className="h-4 w-4" />
          Sign out
        </button>
      </div>
    </>
  )
}

export function Sidebar() {
  const { sidebarOpen, closeSidebar } = useUiStore()

  return (
    <>
      {/* Desktop: static sidebar, part of the flex layout. Narrower in the tight
          md→lg band so it doesn't starve the content (the server view stacks a
          second sidebar next to this one); full width again from lg up. */}
      <aside className="hidden md:flex flex-col md:w-44 lg:w-56 border-r border-border bg-surface h-dvh sticky top-0">
        <SidebarContent />
      </aside>

      {/* Mobile: slide-in drawer with backdrop. */}
      <div
        className={clsx(
          'fixed inset-0 z-50 md:hidden transition-opacity',
          sidebarOpen ? 'opacity-100' : 'pointer-events-none opacity-0',
        )}
      >
        <div className="absolute inset-0 bg-black/60" onClick={closeSidebar} />
        <aside
          className={clsx(
            // Fixed overlays ignore the body's safe-area padding, so the
            // drawer carries its own insets for notched phones.
            'absolute inset-y-0 left-0 flex w-64 max-w-[80%] flex-col border-r border-border bg-surface shadow-xl transition-transform',
            'pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)]',
            sidebarOpen ? 'translate-x-0' : '-translate-x-full',
          )}
        >
          <button
            onClick={closeSidebar}
            aria-label="Close menu"
            className="absolute right-2 top-3 z-10 rounded-md p-2 text-text-secondary hover:bg-surface-2 hover:text-text-primary md:hidden"
          >
            <X className="h-5 w-5" />
          </button>
          <SidebarContent onNavigate={closeSidebar} />
        </aside>
      </div>
    </>
  )
}
