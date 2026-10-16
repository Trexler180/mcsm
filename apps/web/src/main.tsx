import React from 'react'
import ReactDOM from 'react-dom/client'
import { RouterProvider, createRouter } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import * as Tooltip from '@radix-ui/react-tooltip'
import { registerSW } from 'virtual:pwa-register'
import { routeTree } from './routeTree'
import { AppErrorBoundary } from './components/error-boundary'
import { CrashPage } from './shell/crash'
import { bootTheme } from './shell/theme'
// Self-hosted type, so it works offline and under the strict CSP: Geist for
// the interface, Geist Mono for numbers and code, JetBrains Mono for the
// terminal until the console moves to the new design.
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import '@fontsource-variable/jetbrains-mono'
import './index.css'
import './ui/styles/base.css'
import './ui/styles/look.css'
import './ui/styles/legacy.css'

bootTheme()

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
  // A render error inside a route replaces that route's page, not the panel.
  defaultErrorComponent: CrashPage,
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
        {/* Older screens' permission-gated controls explain themselves through
            a Radix tooltip; the new design's controls use data-tip. */}
        <Tooltip.Provider delayDuration={120}>
          <RouterProvider router={router} />
        </Tooltip.Provider>
      </QueryClientProvider>
    </AppErrorBoundary>
  </React.StrictMode>,
)
