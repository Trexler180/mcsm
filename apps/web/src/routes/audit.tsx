import { lazy } from 'react'
import { createRoute } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'
const AuditPage = lazy(() => import('@/pages/audit').then((m) => ({ default: m.AuditPage })))

// Open to everyone: the API shows each person what they're allowed to see.
export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/audit',
  component: AuditPage,
})
