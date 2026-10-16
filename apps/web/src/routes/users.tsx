import { createRoute, redirect } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'
import { AdminOnly } from '@/shell/admin-only'
import { PeoplePage } from '@/pages/people'

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/people',
  component: () => (
    <AdminOnly title="People">
      <PeoplePage />
    </AdminOnly>
  ),
})

/** The old address. */
export const OldUsersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/users',
  beforeLoad: () => {
    throw redirect({ to: '/people', replace: true })
  },
})
