import { lazy, Suspense } from 'react'
import type { FleetServer, MyServerPermissions, Server } from '@/lib/types'
import { ServerPermissionsProvider } from '@/lib/server-permissions'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { Empty, type StatusKey } from '@/ui/controls'
import { deniedTip } from '@/ui/perm'
import { SkForm } from '@/ui/skeleton'
import { cx } from '@/ui/util'
import { allowed } from './data'
import { GeneralSettings } from './settings-general'
import { PropertiesSettings } from './settings-properties'
import { AccessSettings } from './settings-access'

// A server's settings, in four parts down the side: general, version and
// Java, server.properties, and who has access.

const VersionTab = lazy(() => import('@/components/servers/version-migration').then((m) => ({ default: m.VersionTab })))

export type SettingsSection = 'general' | 'version' | 'properties' | 'access'

export function sectionOf(sub: string | undefined): SettingsSection {
  return sub === 'version' || sub === 'properties' || sub === 'access' ? sub : 'general'
}

export function SettingsTab({
  server,
  live,
  status,
  perms,
  section,
  setSection,
}: {
  server: Server
  live: FleetServer | undefined
  status: StatusKey
  perms: MyServerPermissions | undefined
  section: SettingsSection
  setSection: (s: SettingsSection) => void
}) {
  const canAccess = allowed(perms, 'admin')
  const members = useQuery({ queryKey: ['server-members', server.id], queryFn: () => api.servers.members(server.id), enabled: canAccess, staleTime: 60_000, retry: false }).data
  const items: { value: SettingsSection; label: string; count?: number; disabled?: boolean }[] = [
    { value: 'general', label: 'General' },
    { value: 'version', label: 'Version & Java' },
    { value: 'properties', label: 'server.properties' },
    { value: 'access', label: 'Access', count: members?.members.length || undefined, disabled: !canAccess },
  ]
  return (
    <div className="with-subnav">
      <nav className="subnav" aria-label="Settings">
        {items.map((it) => (
          <button
            key={it.value}
            type="button"
            aria-current={section === it.value}
            className={cx(it.disabled && 'disabled')}
            disabled={it.disabled}
            data-tip={it.disabled ? deniedTip('admin') : undefined}
            onClick={() => setSection(it.value)}
          >
            {it.label}
            {it.count ? <span className="aside">{it.count}</span> : null}
          </button>
        ))}
      </nav>
      <div style={{ minWidth: 0 }}>
        {section === 'general' && <GeneralSettings server={server} live={live} status={status} perms={perms} />}
        {section === 'properties' && <PropertiesSettings server={server} status={status} perms={perms} />}
        {section === 'access' && (canAccess ? <AccessSettings server={server} /> : <Empty title="Only people who manage access can change this." />)}
        {section === 'version' && (
          <ServerPermissionsProvider permissions={perms}>
            <Suspense fallback={<SkForm />}>
              <div className="legacy-page" style={{ overflow: 'visible' }}>
                {section === 'version' && <VersionTab server={server} />}
              </div>
            </Suspense>
          </ServerPermissionsProvider>
        )}
      </div>
    </div>
  )
}
