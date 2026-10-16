import { lazy, Suspense, useState, type ReactNode } from 'react'
import { ArrowLeft } from 'lucide-react'
import type { MyServerPermissions, Server, ServerStatus } from '@/lib/types'
import { ServerPermissionsProvider } from '@/lib/server-permissions'
import { TabSkeleton, FilesSkeleton } from '@/ui/skeleton'

// Server tabs not yet on the new design: the previous design's screens,
// shown inside the new server page until each one's turn comes.

const WorldsTab = lazy(() => import('@/components/servers/worlds-tab').then((m) => ({ default: m.WorldsTab })))
const FileBrowser = lazy(() => import('@/components/files/browser').then((m) => ({ default: m.FileBrowser })))
const FileEditor = lazy(() => import('@/components/files/editor').then((m) => ({ default: m.FileEditor })))
const DatViewer = lazy(() => import('@/components/files/dat-viewer').then((m) => ({ default: m.DatViewer })))

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

export function LegacyTab({ tab, server, perms }: { tab: 'worlds' | 'files'; server: Server; perms: MyServerPermissions | undefined }) {
  const status = server.status as ServerStatus
  return (
    <ServerPermissionsProvider permissions={perms}>
      <Frame fill tab={tab}>
        {tab === 'worlds' ? <WorldsTab serverId={server.id} status={status} /> : <Files serverId={server.id} />}
      </Frame>
    </ServerPermissionsProvider>
  )
}
