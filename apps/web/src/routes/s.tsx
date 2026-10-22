import { lazy } from 'react'
import { createRoute, useNavigate, useParams, useSearch } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'
import type { ServerSearch } from '@/pages/server/page'

const ServerPage = lazy(() => import('@/pages/server/page').then((m) => ({ default: m.ServerPage })))

// A server's pages: /s/<id> is its console, /s/<id>/<tab> the rest.

function validateSearch(search: Record<string, unknown>): ServerSearch {
  const out: ServerSearch = {}
  if (search.view === 'files' || search.view === 'problems') out.view = search.view
  if (typeof search.sub === 'string' && search.sub) out.sub = search.sub
  if (typeof search.player === 'string' && search.player) out.player = search.player
  if (typeof search.path === 'string' && search.path.startsWith('/')) out.path = search.path
  if (typeof search.open === 'string' && search.open.startsWith('/')) out.open = search.open
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
      setSearch={(next, opts) => void navigate({ to: '.', search: next as never, replace: !opts?.push })}
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
