import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { FleetNode, FleetServer, FleetSummary, ServerFolder, ServerPlacement } from '@/lib/types'
import type { StatusKey } from '@/ui/controls'
import { plural } from '@/ui/util'

// The fleet as the shell sees it: every server the person can see, its
// folder, its live numbers, and the nodes they run on. One request (/fleet),
// kept current by the live event stream.

export function useFleet() {
  return useQuery({ queryKey: ['fleet'], queryFn: () => api.fleet(), staleTime: 30_000 })
}

export function useFolders() {
  return useQuery({ queryKey: ['server-folders'], queryFn: ({ signal }) => api.serverFolders.list(signal), staleTime: 60_000 })
}

/** A server's state as people should read it: a server whose node isn't
 *  answering is "Node offline", never "Stopped" or "Crashed". */
export function effStatus(s: Pick<FleetServer, 'status' | 'node_id'>, nodes: FleetNode[] | undefined): StatusKey {
  if (s.status === 'unknown') return 'unreachable'
  const node = nodes?.find((n) => n.id === s.node_id)
  const live = s.status === 'online' || s.status === 'starting'
  if (node && !node.online && !live) return 'unreachable'
  return s.status as StatusKey
}

/** What the sidebar says next to a server's name. */
export function serverAside(s: FleetServer, nodes: FleetNode[] | undefined): { text: string; tone?: 'bad' | 'warn'; tip?: string } | null {
  const es = effStatus(s, nodes)
  if (es === 'online') {
    const n = s.players ?? 0
    return { text: String(n), tip: plural(n, 'player') + ' online' }
  }
  if (es === 'crashed' || es === 'startup_failure') return { text: es === 'crashed' ? 'crashed' : 'failed', tone: 'bad' }
  if (es === 'starting' || es === 'stopping') return { text: es, tone: 'warn' }
  if (es === 'unreachable') return { text: 'node offline' }
  return null
}

const bySort = (a: FleetServer, b: FleetServer) => a.sort_order - b.sort_order || a.name.localeCompare(b.name)

/** Servers grouped by folder in sidebar order. Servers in a folder the person
 *  can't see, or in none, are in the trailing group (folder null). */
export function groupFleet(servers: FleetServer[], folders: ServerFolder[]): { folder: ServerFolder | null; servers: FleetServer[] }[] {
  const known = new Set(folders.map((f) => f.id))
  const ordered = [...folders].sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name))
  const groups = ordered.map((folder) => ({ folder: folder as ServerFolder | null, servers: servers.filter((s) => s.folder_id === folder.id).sort(bySort) }))
  groups.push({ folder: null, servers: servers.filter((s) => !s.folder_id || !known.has(s.folder_id)).sort(bySort) })
  return groups
}

/** Where every server in the target folder ends up when one is dropped at
 *  `index` in it. */
export function placeServer(fleet: FleetSummary, folders: ServerFolder[], serverId: string, folderId: string | null, index: number): ServerPlacement[] {
  const group = groupFleet(fleet.servers, folders).find((g) => (g.folder?.id ?? null) === folderId)
  const list = (group?.servers ?? []).map((s) => s.id)
  const cur = list.indexOf(serverId)
  let at = index
  if (cur !== -1) {
    list.splice(cur, 1)
    if (cur < at) at--
  }
  list.splice(Math.max(0, Math.min(at, list.length)), 0, serverId)
  return list.map((id, i) => ({ id, folder_id: folderId, sort_order: i }))
}

/** The fleet with servers moved, for showing a move before the API answers. */
export function withPlacement(fleet: FleetSummary, placement: ServerPlacement[]): FleetSummary {
  const at = new Map(placement.map((p) => [p.id, p]))
  return {
    ...fleet,
    servers: fleet.servers.map((s) => {
      const p = at.get(s.id)
      return p ? { ...s, folder_id: p.folder_id, sort_order: p.sort_order } : s
    }),
  }
}
