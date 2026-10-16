import type { ServerPermission } from '@/lib/types'

/** The tabs inside a server, and what each needs. Console is its front page. */
export const SERVER_TABS: { value: ServerTab; label: string; perm: ServerPermission }[] = [
  { value: 'console', label: 'Console', perm: 'console' },
  { value: 'players', label: 'Players', perm: 'players' },
  { value: 'mods', label: 'Mods', perm: 'mods' },
  { value: 'worlds', label: 'Worlds', perm: 'files' },
  { value: 'files', label: 'Files', perm: 'files' },
  { value: 'backups', label: 'Backups', perm: 'backups' },
  { value: 'schedules', label: 'Schedules', perm: 'tasks' },
  { value: 'stats', label: 'Stats', perm: 'view' },
  { value: 'settings', label: 'Settings', perm: 'settings' },
]

export type ServerTab = 'console' | 'players' | 'mods' | 'worlds' | 'files' | 'backups' | 'schedules' | 'stats' | 'settings'

export const isServerTab = (v: string | undefined): v is ServerTab => !!v && SERVER_TABS.some((t) => t.value === v)

/** Where an address from the previous design's server page now lands. */
export function tabForOldSection(section: string): { tab: ServerTab; sub?: string } {
  switch (section) {
    case 'console':
    case 'dashboard':
      return { tab: 'console' }
    case 'logs':
      return { tab: 'console', sub: 'files' }
    case 'tasks':
      return { tab: 'schedules' }
    case 'options':
    case 'software':
      return { tab: 'settings' }
    case 'properties':
      return { tab: 'settings', sub: 'properties' }
    case 'version':
      return { tab: 'settings', sub: 'version' }
    case 'access':
      return { tab: 'settings', sub: 'access' }
    case 'configs':
      return { tab: 'mods', sub: 'configs' }
    default:
      return isServerTab(section) ? { tab: section } : { tab: 'console' }
  }
}
