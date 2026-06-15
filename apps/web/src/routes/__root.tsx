import { createRootRoute, Outlet, useNavigate } from '@tanstack/react-router'
import { useEffect } from 'react'
import { Sidebar } from '@/components/layout/sidebar'
import { Toaster } from '@/components/ui/toast'
import { useAuthStore } from '@/store/auth'

function RootLayout() {
  const { isAuthenticated, isLoading, init } = useAuthStore()
  const navigate = useNavigate()

  useEffect(() => {
    init()
  }, [])

  useEffect(() => {
    if (!isLoading && !isAuthenticated && window.location.pathname !== '/login') {
      navigate({ to: '/login' })
    }
  }, [isAuthenticated, isLoading])

  if (isLoading) {
    return (
      <div className="flex h-dvh items-center justify-center bg-background">
        <div className="flex flex-col items-center gap-3">
          <div className="w-8 h-8 border-2 border-accent border-t-transparent rounded-full animate-spin" />
          <p className="text-sm text-text-secondary">Loading...</p>
        </div>
      </div>
    )
  }

  // Login route renders without sidebar
  if (!isAuthenticated) {
    return (
      <>
        <Outlet />
        <Toaster />
      </>
    )
  }

  return (
    // h-dvh (not h-screen): mobile browsers resize dvh as the URL bar
    // collapses, so the layout never hides behind it.
    <div className="flex h-dvh bg-background overflow-hidden">
      <Sidebar />
      <main className="flex-1 flex flex-col min-w-0 overflow-y-auto">
        <Outlet />
      </main>
      <Toaster />
    </div>
  )
}

export const Route = createRootRoute({
  component: RootLayout,
})
