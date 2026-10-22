import { lazy } from 'react'
import { createRoute } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'
import { AdminOnly } from '@/shell/admin-only'
const NodesPage = lazy(() => import('@/pages/nodes').then((m) => ({ default: m.NodesPage })))

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/nodes',
  component: () => (
    <AdminOnly title="Nodes">
      <NodesPage />
    </AdminOnly>
  ),
})
