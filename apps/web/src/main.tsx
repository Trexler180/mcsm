import React from 'react'
import ReactDOM from 'react-dom/client'
import { RouterProvider, createRouter } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { registerSW } from 'virtual:pwa-register'
import { routeTree } from './routeTree'
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
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </React.StrictMode>,
)
