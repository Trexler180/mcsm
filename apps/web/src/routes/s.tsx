import { createRoute, useNavigate, useParams, useSearch } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'
import { ServerPage, type ServerSearch } from '@/pages/server/page'

// A server's pages: /s/<id> is its console, /s/<id>/<tab> the rest.

function validateSearch(search: Record<string, unknown>): ServerSearch {
  const out: ServerSearch = {}
  if (search.view === 'files' || search.view === 'problems') out.view = search.view
  if (typeof search.sub === 'string' && search.sub) out.sub = search.sub
  return out
}

function ServerRouteView() {
  const params = useParams({ strict: false }) as { id: string; tab?: string }
  const search = useSearch({ strict: false }) as ServerSearch
  const navigate = useNavigate()
  return (
    <ServerPage
      key={params.id}
      id={params.id}
      tab={params.tab}
      search={search}
      setSearch={(next) => void navigate({ to: '.', search: next as never, replace: true })}
    />
  )
}

export const ServerRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/s/$id',
  validateSearch,
  component: ServerRouteView,
})

export const ServerTabRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/s/$id/$tab',
  validateSearch,
  component: ServerRouteView,
})
