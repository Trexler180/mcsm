import { createRootRoute, Outlet, useNavigate } from '@tanstack/react-router'
import { Suspense, useEffect } from 'react'
import { AppShell, BootScreen } from '@/shell/app-shell'
import { TipHost } from '@/ui/overlays'
import { Toasts } from '@/ui/toasts'
import { useAuthStore } from '@/store/auth'
import { rememberReturnTo } from '@/lib/return-to'

// Pages load on first visit; this holds their place for the moment that takes.
function PageLoading() {
  return <div className="scroll" aria-busy="true" />
}

function RootLayout() {
  const { isAuthenticated, isLoading, init } = useAuthStore()
  const navigate = useNavigate()

  useEffect(() => {
    init()
    // Once, on first load.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    // navigate() is basepath-aware; the guard must compare against the
    // base-prefixed login path so it matches under a subpath deployment.
    const loginPath = import.meta.env.BASE_URL + 'login'
    if (!isLoading && !isAuthenticated && window.location.pathname !== loginPath) {
      // Keep the destination so login can return there. The OAuth consent
      // screen depends on it: its request id lives in the query string and the
      // request is single-use, so dropping it strands the connection attempt.
      rememberReturnTo(window.location.pathname, window.location.search)
      navigate({ to: '/login' })
    }
  }, [isAuthenticated, isLoading, navigate])

  if (isLoading) return <BootScreen />

  // Sign-in renders without the shell.
  if (!isAuthenticated) {
    return (
      <>
        <Suspense fallback={<PageLoading />}>
          <Outlet />
        </Suspense>
        <Toasts />
        <TipHost />
      </>
    )
  }

  return (
    <AppShell>
      <Suspense fallback={<PageLoading />}>
        <Outlet />
      </Suspense>
    </AppShell>
  )
}

export const Route = createRootRoute({
  component: RootLayout,
})
