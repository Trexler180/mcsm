import { createRoute, redirect, useNavigate, useParams } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'
import { AlertsPage, type AlertsTab } from '@/pages/alerts'

// Alerts: /alerts is the inbox, /alerts/rules and /alerts/delivery the rest.
// The old /notifications address lands on the inbox.

function AlertsView() {
  const { tab } = useParams({ strict: false }) as { tab?: string }
  const navigate = useNavigate()
  const current: AlertsTab = tab === 'rules' || tab === 'delivery' ? tab : 'inbox'
  return <AlertsPage tab={current} setTab={(t) => void navigate({ to: t === 'inbox' ? '/alerts' : `/alerts/${t}` as '/alerts' })} />
}

export const AlertsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/alerts',
  component: AlertsView,
})

export const AlertsTabRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/alerts/$tab',
  component: AlertsView,
})

export const OldNotificationsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/notifications',
  beforeLoad: () => {
    throw redirect({ to: '/alerts', replace: true })
  },
})
