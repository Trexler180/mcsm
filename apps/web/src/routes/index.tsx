import { createRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Server, Activity, Users } from 'lucide-react'
import { Route as rootRoute } from './__root'
import { Header } from '@/components/layout/header'
import { Card, CardContent } from '@/components/ui/card'
import { StatusBadge } from '@/components/ui/badge'
import { api } from '@/lib/api'
import type { ServerStatus } from '@/lib/types'

function DashboardPage() {
  const { data: servers = [] } = useQuery({
    queryKey: ['servers'],
    queryFn: () => api.servers.list(),
    refetchInterval: 10_000,
  })

  const online = servers.filter((s) => s.status === 'online').length
  const starting = servers.filter(
    (s) => s.status === 'starting' || s.status === 'stopping',
  ).length

  const stats = [
    { label: 'Total Servers', value: servers.length, icon: Server },
    { label: 'Online', value: online, icon: Activity, accent: true },
    { label: 'In Transition', value: starting, icon: Users },
  ]

  return (
    <div>
      <Header title="Dashboard" />
      <div className="p-4 sm:p-6 space-y-6">
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          {stats.map((s) => (
            <Card key={s.label}>
              <CardContent className="flex items-center gap-4 py-4">
                <div
                  className={`w-10 h-10 rounded-lg flex items-center justify-center ${s.accent ? 'bg-accent/20' : 'bg-surface-2'}`}
                >
                  <s.icon className={`h-5 w-5 ${s.accent ? 'text-accent' : 'text-text-secondary'}`} />
                </div>
                <div>
                  <p className="text-2xl font-bold text-text-primary">{s.value}</p>
                  <p className="text-sm text-text-secondary">{s.label}</p>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>

        <Card>
          <div className="border-b border-border px-5 py-4">
            <h2 className="text-sm font-semibold text-text-primary">Servers</h2>
          </div>
          <div className="divide-y divide-border">
            {servers.length === 0 && (
              <p className="px-5 py-8 text-center text-sm text-text-secondary">
                No servers yet. Create one to get started.
              </p>
            )}
            {servers.map((srv) => (
              <div key={srv.id} className="flex items-center justify-between gap-3 px-4 sm:px-5 py-3">
                <div className="flex min-w-0 items-center gap-3">
                  <StatusBadge status={srv.status as ServerStatus} />
                  <span className="truncate text-sm font-medium text-text-primary">{srv.name}</span>
                  <span className="hidden truncate text-xs text-text-secondary sm:inline">{srv.platform} {srv.mc_version}</span>
                </div>
                <span className="flex-shrink-0 text-xs text-text-secondary">:{srv.port}</span>
              </div>
            ))}
          </div>
        </Card>
      </div>
    </div>
  )
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: DashboardPage,
})
