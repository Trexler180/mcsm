import { useCallback } from 'react'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { sectionOf } from './paths'

/** Navigates to a panel path such as paths.server(id). */
export function useGo() {
  const navigate = useNavigate()
  // Paths come from paths.ts and match the route tree at runtime; the router's
  // literal route types can't express them as plain strings.
  return useCallback((to: string) => void navigate({ to: to as '/' }), [navigate])
}

/** The sidebar entry for the current address. */
export function useSection() {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  return { pathname, ...sectionOf(pathname) }
}
