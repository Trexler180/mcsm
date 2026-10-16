// Where the shell links to. Screens move to the new design one at a time;
// each one's address changes here when its new page lands, and the old
// address redirects.

export const paths = {
  home: '/',
  alerts: '/notifications',
  nodes: '/nodes',
  people: '/users',
  audit: '/audit',
  settings: '/settings',
  account: '/account',
  newServer: '/servers',
  /** A server's page; Console is its front page. */
  server: (id: string, tab?: string) => `/s/${encodeURIComponent(id)}${tab && tab !== 'console' ? '/' + tab : ''}`,
} as const

/** Which sidebar entry an address belongs to. */
export function sectionOf(pathname: string): { section: string; serverId?: string } {
  const base = import.meta.env.BASE_URL.replace(/\/$/, '')
  const p = pathname.startsWith(base) ? pathname.slice(base.length) || '/' : pathname
  const parts = p.split('/').filter(Boolean)
  if (parts.length === 0) return { section: 'home' }
  if (parts[0] === 'servers' && parts[1]) return { section: 'server', serverId: decodeURIComponent(parts[1]) }
  if (parts[0] === 's' && parts[1]) return { section: 'server', serverId: decodeURIComponent(parts[1]) }
  const map: Record<string, string> = {
    notifications: 'alerts',
    alerts: 'alerts',
    nodes: 'nodes',
    users: 'people',
    people: 'people',
    audit: 'audit',
    settings: 'settings',
    account: 'account',
    servers: 'servers',
  }
  return { section: map[parts[0]] ?? parts[0] }
}
