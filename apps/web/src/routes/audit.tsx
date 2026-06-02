import { createRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Route as rootRoute } from './__root'
import { Header } from '@/components/layout/header'
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from '@/components/ui/table'
import { api } from '@/lib/api'

function actionBadge(action: string) {
  const color = action.startsWith('server.')
    ? 'bg-blue-500/15 text-blue-400 border-blue-500/30'
    : action.startsWith('mod.')
      ? 'bg-purple-500/15 text-purple-400 border-purple-500/30'
      : action.startsWith('auth.')
        ? 'bg-green-500/15 text-green-400 border-green-500/30'
        : 'bg-surface-2 text-text-secondary border-border'
  return (
    <span className={`text-xs px-2 py-0.5 rounded border font-mono ${color}`}>
      {action}
    </span>
  )
}

function AuditPage() {
  const { data: entries = [], isLoading } = useQuery({
    queryKey: ['audit'],
    queryFn: () => api.audit.list(200),
    refetchInterval: 30_000,
  })

  return (
    <div>
      <Header
        title="Audit Log"
        description={`${entries.length} recent action${entries.length !== 1 ? 's' : ''}`}
      />
      <div className="p-6">
        {isLoading ? (
          <div className="flex justify-center py-16">
            <div className="w-6 h-6 border-2 border-accent border-t-transparent rounded-full animate-spin" />
          </div>
        ) : entries.length === 0 ? (
          <p className="text-text-secondary text-sm py-8 text-center">
            No audit entries yet.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Time</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>User</TableHead>
                <TableHead>Server</TableHead>
                <TableHead>IP</TableHead>
                <TableHead>Detail</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.map((e) => (
                <TableRow key={e.id}>
                  <TableCell className="text-text-secondary text-sm whitespace-nowrap">
                    {new Date(e.created_at).toLocaleString()}
                  </TableCell>
                  <TableCell>{actionBadge(e.action)}</TableCell>
                  <TableCell className="text-text-secondary text-xs font-mono">
                    {e.user_id?.slice(0, 8) ?? '—'}
                  </TableCell>
                  <TableCell className="text-text-secondary text-xs font-mono">
                    {e.server_id?.slice(0, 8) ?? '—'}
                  </TableCell>
                  <TableCell className="text-text-secondary text-sm">
                    {e.ip_address ?? '—'}
                  </TableCell>
                  <TableCell className="text-text-secondary text-xs font-mono max-w-xs truncate">
                    {e.detail && e.detail !== 'null' ? e.detail : '—'}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>
    </div>
  )
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/audit',
  component: AuditPage,
})
