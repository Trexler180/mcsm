import React from 'react'
import ReactDOM from 'react-dom/client'
import { RouterProvider, createRouter } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import * as Tooltip from '@radix-ui/react-tooltip'
import { registerSW } from 'virtual:pwa-register'
import { routeTree } from './routeTree'
import { AppErrorBoundary, RouteErrorFallback } from './components/error-boundary'
// Self-hosted mono (variable, one file for all weights). Referenced by
// fontFamily.mono — used across the console, editors, audit log and charts.
// Self-hosted rather than a CDN so it works offline and under the strict CSP.
import '@fontsource-variable/jetbrains-mono'
import './index.css'

// Auto-update service worker: new deployments activate on the next load
// without prompting. No-op in dev where no SW is generated.
registerSW({ immediate: true })

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      retry: 1,
    },
  },
})

const router = createRouter({
  routeTree,
  // Honour the build-time base path (Vite's BASE_URL, set via VITE_BASE) so
  // routing works when the app is served from a subpath like '/dashboard/'.
  // Defaults to '/' for local dev.
  basepath: import.meta.env.BASE_URL,
  context: { queryClient },
  // A render error inside a route replaces that route's outlet instead of
  // blanking the whole app.
  defaultErrorComponent: RouteErrorFallback,
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <AppErrorBoundary>
      <QueryClientProvider client={queryClient}>
        {/* Permission-gated controls explain themselves through a tooltip, and
            they appear on nearly every server view — so the provider belongs at
            the root rather than in each panel that happens to hold one. */}
        <Tooltip.Provider delayDuration={120}>
          <RouterProvider router={router} />
        </Tooltip.Provider>
      </QueryClientProvider>
    </AppErrorBoundary>
  </React.StrictMode>,
)
