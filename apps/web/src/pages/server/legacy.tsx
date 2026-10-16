import { lazy, Suspense, useState, type ReactNode } from 'react'
import { ArrowLeft } from 'lucide-react'
import type { MyServerPermissions, Server, ServerStatus } from '@/lib/types'
import { ServerPermissionsProvider } from '@/lib/server-permissions'
import { Tabs } from '@/ui/controls'
import { TabSkeleton, FilesSkeleton } from '@/ui/skeleton'
import { allowed } from './data'
import type { ServerTab } from './tabs'

// Server tabs not yet on the new design: the previous design's screens,
// shown inside the new server page until each one's turn comes.

const PlayersPanel = lazy(() => import('@/components/players/panel').then((m) => ({ default: m.PlayersPanel })))
const ModSearch = lazy(() => import('@/components/mods/search').then((m) => ({ default: m.ModSearch })))
const ConfigsTab = lazy(() => import('@/components/configs/configs-tab').then((m) => ({ default: m.ConfigsTab })))
const WorldsTab = lazy(() => import('@/components/servers/worlds-tab').then((m) => ({ default: m.WorldsTab })))
const FileBrowser = lazy(() => import('@/components/files/browser').then((m) => ({ default: m.FileBrowser })))
const FileEditor = lazy(() => import('@/components/files/editor').then((m) => ({ default: m.FileEditor })))
const DatViewer = lazy(() => import('@/components/files/dat-viewer').then((m) => ({ default: m.DatViewer })))
const BackupsTab = lazy(() => import('@/components/servers/backups-tab').then((m) => ({ default: m.BackupsTab })))
const TasksTab = lazy(() => import('@/components/servers/tasks-tab').then((m) => ({ default: m.TasksTab })))
const StatsTab = lazy(() => import('@/components/servers/stats-tab').then((m) => ({ default: m.StatsTab })))
const OptionsTab = lazy(() => import('@/components/servers/options/options-tab').then((m) => ({ default: m.OptionsTab })))
const PropertiesTab = lazy(() => import('@/components/servers/options/properties-panel').then((m) => ({ default: m.PropertiesTab })))
const VersionTab = lazy(() => import('@/components/servers/version-migration').then((m) => ({ default: m.VersionTab })))
const AccessTab = lazy(() => import('@/components/servers/access-tab').then((m) => ({ default: m.AccessTab })))

function Frame({ children, fill, tab }: { children: ReactNode; fill?: boolean; tab: string }) {
  return (
    <div className="legacy-page" style={fill ? { overflow: 'hidden' } : undefined}>
      <Suspense
        fallback={
          <div className="page" style={{ paddingTop: 22 }}>
            {tab === 'files' ? <FilesSkeleton /> : <TabSkeleton tab={tab} />}
          </div>
        }
      >
        <div className={fill ? 'flex h-full min-h-0 flex-1 flex-col' : 'p-4 sm:p-6'}>{children}</div>
      </Suspense>
    </div>
  )
}

function Files({ serverId }: { serverId: string }) {
  const [selected, setSelected] = useState<string | null>(null)
  return (
    <div className="flex h-full min-w-0">
      <div className={`${selected ? 'hidden xl:flex' : 'flex'} w-full flex-shrink-0 flex-col overflow-hidden border-border xl:w-80 xl:border-r`}>
        <FileBrowser serverId={serverId} onFileSelect={(path) => setSelected(path)} />
      </div>
      <div className={`${selected ? 'flex' : 'hidden xl:flex'} min-w-0 flex-1 flex-col overflow-hidden`}>
        {selected ? (
          <>
            <button
              onClick={() => setSelected(null)}
              className="flex flex-shrink-0 items-center gap-1.5 border-b border-border bg-surface px-4 py-2 text-sm text-text-secondary hover:text-text-primary xl:hidden"
            >
              <ArrowLeft className="h-4 w-4" /> Back to files
            </button>
            <div className="min-h-0 flex-1 overflow-hidden">
              {/\.(dat|dat_old|nbt)$/i.test(selected) ? <DatViewer serverId={serverId} path={selected} /> : <FileEditor serverId={serverId} path={selected} />}
            </div>
          </>
        ) : (
          <div className="flex h-full items-center justify-center text-text-secondary">
            <p className="text-sm">Select a file to edit</p>
          </div>
        )}
      </div>
    </div>
  )
}

export function LegacyTab({
  tab,
  sub,
  setSub,
  server,
  perms,
}: {
  tab: Exclude<ServerTab, 'console'>
  sub?: string
  setSub: (sub: string | undefined) => void
  server: Server
  perms: MyServerPermissions | undefined
}) {
  const status = server.status as ServerStatus
  let body: ReactNode = null
  let fill = false
  switch (tab) {
    case 'players':
      body = <PlayersPanel serverId={server.id} status={status} />
      fill = true
      break
    case 'mods': {
      const view = sub === 'configs' ? 'configs' : 'mods'
      body = (
        <>
          <div className="px-4 pt-3 sm:px-6">
            <Tabs
              sub
              value={view}
              onChange={(v) => setSub(v === 'mods' ? undefined : v)}
              items={[
                { value: 'mods', label: 'Installed and browse' },
                { value: 'configs', label: 'Configs', disabled: !allowed(perms, 'files') },
              ]}
            />
          </div>
          <div className="flex min-h-0 flex-1 flex-col">
            {view === 'configs' ? <ConfigsTab serverId={server.id} /> : <ModSearch serverId={server.id} loader={server.platform} mcVersion={server.mc_version} platform={server.platform} />}
          </div>
        </>
      )
      fill = true
      break
    }
    case 'worlds':
      body = <WorldsTab serverId={server.id} status={status} />
      fill = true
      break
    case 'files':
      body = <Files serverId={server.id} />
      fill = true
      break
    case 'backups':
      body = <BackupsTab serverId={server.id} />
      break
    case 'schedules':
      body = <TasksTab serverId={server.id} />
      break
    case 'stats':
      body = <StatsTab serverId={server.id} ramMaxMb={server.ram_mb_max} />
      break
    case 'settings': {
      const view = sub === 'version' || sub === 'properties' || sub === 'access' ? sub : 'general'
      body = (
        <>
          <div style={{ marginBottom: 16 }}>
            <Tabs
              sub
              value={view}
              onChange={(v) => setSub(v === 'general' ? undefined : v)}
              items={[
                { value: 'general', label: 'General' },
                { value: 'version', label: 'Version & Java' },
                { value: 'properties', label: 'server.properties' },
                { value: 'access', label: 'Access', disabled: !allowed(perms, 'admin') },
              ]}
            />
          </div>
          {view === 'general' && <OptionsTab server={server} />}
          {view === 'version' && <VersionTab server={server} />}
          {view === 'properties' && (
            <div data-server-scroll>
              <PropertiesTab server={server} />
            </div>
          )}
          {view === 'access' && <AccessTab serverId={server.id} />}
        </>
      )
      break
    }
  }
  return (
    <ServerPermissionsProvider permissions={perms}>
      <Frame fill={fill} tab={tab}>
        {body}
      </Frame>
    </ServerPermissionsProvider>
  )
}
