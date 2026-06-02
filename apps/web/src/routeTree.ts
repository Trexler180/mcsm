import { Route as rootRoute } from './routes/__root'
import { Route as loginRoute } from './routes/login'
import { Route as indexRoute } from './routes/index'
import { Route as serversRoute } from './routes/servers/index'
import { Route as serverDetailRoute } from './routes/servers/$id'
import { Route as nodesRoute } from './routes/nodes'
import { Route as usersRoute } from './routes/users'
import { Route as auditRoute } from './routes/audit'

export const routeTree = rootRoute.addChildren([
  loginRoute,
  indexRoute,
  serversRoute,
  serverDetailRoute,
  nodesRoute,
  usersRoute,
  auditRoute,
])
