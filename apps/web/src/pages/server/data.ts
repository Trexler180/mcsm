import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { can } from '@/lib/permissions'
import type { FleetServer, MyServerPermissions, Server, ServerPermission } from '@/lib/types'
import { effStatus, useFleet } from '@/shell/fleet'
import type { StatusKey } from '@/ui/controls'

// What a server's pages read. Each is one query, shared by key with the rest
// of the panel, so the header, the console and the tabs never disagree.

export function useServer(id: string) {
  return useQuery({ queryKey: ['server', id], queryFn: () => api.servers.get(id), staleTime: 30_000 })
}

export function useMyPerms(id: string) {
  return useQuery({ queryKey: ['server-permissions', id], queryFn: () => api.servers.myPermissions(id), staleTime: 60_000 })
}

export function useIdentity(id: string, enabled = true) {
  return useQuery({ queryKey: ['server-identity', id], queryFn: () => api.servers.identity(id), staleTime: 5 * 60_000, enabled, retry: false })
}

/** The server's live entry in the fleet: players, TPS, CPU, memory, the
 *  operation running on it. Updated by the event stream. */
export function useFleetServer(id: string): { server: FleetServer | undefined; status: StatusKey | undefined; nodeName: string; nodeOnline: boolean } {
  const fleet = useFleet().data
  const server = fleet?.servers.find((s) => s.id === id)
  const node = server ? fleet?.nodes.find((n) => n.id === server.node_id) : undefined
  return {
    server,
    status: server ? effStatus(server, fleet?.nodes) : undefined,
    nodeName: node?.name ?? '',
    nodeOnline: node?.online ?? true,
  }
}

/** The status to show: the fleet's (live, node-aware) when there is one,
 *  otherwise the server record's. */
export function statusOf(server: Server, live: StatusKey | undefined): StatusKey {
  if (live) return live
  return server.status === 'unknown' ? 'unreachable' : (server.status as StatusKey)
}

export const isLiveStatus = (s: StatusKey) => s === 'online' || s === 'starting'
export const isBusyStatus = (s: StatusKey) => s === 'starting' || s === 'stopping'
export const failedStatus = (s: StatusKey) => s === 'crashed' || s === 'startup_failure'

export function allowed(perms: MyServerPermissions | undefined, need: ServerPermission) {
  return can(perms, need)
}

/** Why a server didn't start, when it didn't. */
export function useStartupProblem(id: string, enabled: boolean) {
  return useQuery({ queryKey: ['startup-problem', id], queryFn: () => api.servers.startupProblem(id), enabled, staleTime: 15_000, retry: false })
}
