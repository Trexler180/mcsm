import { createRoute } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'
import { AdminOnly } from '@/shell/admin-only'
import { PanelSettingsPage } from '@/pages/panel-settings'

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: () => (
    <AdminOnly title="Settings">
      <PanelSettingsPage />
    </AdminOnly>
  ),
})
