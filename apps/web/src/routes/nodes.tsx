import { createRoute } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'
import { AdminOnly } from '@/shell/admin-only'
import { NodesPage } from '@/pages/nodes'

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/nodes',
  component: () => (
    <AdminOnly title="Nodes">
      <NodesPage />
    </AdminOnly>
  ),
})
