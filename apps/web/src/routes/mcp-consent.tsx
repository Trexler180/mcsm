import { createRoute, lazyRouteComponent } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'

const ConsentPage = lazyRouteComponent(
  () => import('./mcp-consent.page'),
  'ConsentPage',
)

// The OAuth consent screen. A normal authenticated route on purpose: an
// unauthenticated visitor is sent to login and returned here afterwards, so
// "there is a live human session" is enforced by the machinery that already
// guards every other page rather than by anything new.
//
// Component code-split, like the other pages: nobody loads this until a client
// actually asks to connect.
export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/mcp-consent',
  component: () => <ConsentPage />,
})
