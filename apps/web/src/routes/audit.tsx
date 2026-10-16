import { createRoute } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'
import { AuditPage } from '@/pages/audit'

// Open to everyone: the API shows each person what they're allowed to see.
export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/audit',
  component: AuditPage,
})
