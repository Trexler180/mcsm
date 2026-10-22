import { lazy } from 'react'
import { createRoute } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'
import { AdminOnly } from '@/shell/admin-only'
const PanelSettingsPage = lazy(() => import('@/pages/panel-settings').then((m) => ({ default: m.PanelSettingsPage })))

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: () => (
    <AdminOnly title="Settings">
      <PanelSettingsPage />
    </AdminOnly>
  ),
})
