import { Route as rootRoute } from './routes/__root'
import { Route as loginRoute } from './routes/login'
import { Route as indexRoute } from './routes/index'
import { Route as serversRoute } from './routes/servers/index'
import {
  Route as serverDetailRoute,
  ServerIndexRedirectRoute as serverDetailRedirectRoute,
} from './routes/servers/$id'
import { Route as nodesRoute } from './routes/nodes'
import { Route as usersRoute } from './routes/users'
import { Route as auditRoute } from './routes/audit'
import { Route as settingsRoute } from './routes/settings'
import { Route as accountRoute } from './routes/account'
import { AlertsRoute, AlertsTabRoute, OldNotificationsRoute } from './routes/alerts'
import { Route as mcpConsentRoute } from './routes/mcp-consent'
import { ServerRoute, ServerTabRoute } from './routes/s'

export const routeTree = rootRoute.addChildren([
  loginRoute,
  indexRoute,
  serversRoute,
  serverDetailRedirectRoute,
  serverDetailRoute,
  ServerRoute,
  ServerTabRoute,
  nodesRoute,
  usersRoute,
  auditRoute,
  settingsRoute,
  accountRoute,
  AlertsRoute,
  AlertsTabRoute,
  OldNotificationsRoute,
  mcpConsentRoute,
])
