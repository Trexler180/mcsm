import { lazy } from 'react'
import { createRoute, redirect } from '@tanstack/react-router'
import { Route as rootRoute } from '../__root'
import { AdminOnly } from '@/shell/admin-only'
const NewServerPage = lazy(() => import('@/pages/new-server').then((m) => ({ default: m.NewServerPage })))

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/new',
  component: () => (
    <AdminOnly title="Add a server">
      <NewServerPage />
    </AdminOnly>
  ),
})

/** The old server list: Home lists servers now. */
export const OldServersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/servers',
  beforeLoad: () => {
    throw redirect({ to: '/', replace: true })
  },
})
