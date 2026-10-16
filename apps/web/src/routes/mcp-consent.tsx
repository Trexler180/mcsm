import { createRoute } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'
import { ConsentPage } from '@/pages/consent'

// The OAuth consent screen. A normal signed-in route on purpose: someone not
// signed in is sent to sign in and brought back here, so "a person is here"
// is enforced by what already guards every other page.
export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/mcp-consent',
  component: ConsentPage,
})
