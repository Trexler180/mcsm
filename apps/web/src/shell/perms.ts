import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { MyServerPermissions } from '@/lib/types'
import { useAuthStore } from '@/store/auth'

/** What the signed-in person may do on each server, for controls on pages
 *  that list many servers. The server list carries each one's permissions;
 *  administrators may do everything. */
export function useServerPerms(): (serverId: string) => MyServerPermissions | null {
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  const servers = useQuery({ queryKey: ['servers'], queryFn: ({ signal }) => api.servers.list(signal), staleTime: 30_000, enabled: !admin }).data
  return (id) => {
    if (admin) return { owner: false, global_admin: true, permissions: [] }
    const s = servers?.find((x) => x.id === id)
    if (!s) return null
    return { owner: s.owner_id === useAuthStore.getState().user?.id, global_admin: false, permissions: s.permissions ?? [] }
  }
}
