import { lazy, Suspense } from 'react'
import { ApiError } from '@/lib/api'
import { useGo } from '@/shell/nav'
import { paths } from '@/shell/paths'
import { Btn, Empty } from '@/ui/controls'
import { failureOf } from '@/ui/failure'
import { PermScope, deniedTip } from '@/ui/perm'
import { ConsoleSkeleton, LoadError, Sk } from '@/ui/skeleton'
import { ConsoleTab, type ConsoleView } from './console'
import { allowed, failedStatus, statusOf, useFleetServer, useMyPerms, useServer, useStartupProblem } from './data'
import { ServerHeader, tabLabel } from './header'
import { SERVER_TABS, isServerTab, modsViewOf, parentOf, sectionOf, type ServerTab } from './tabs'

// Every tab but the console (the one a server opens on) loads on first visit.
const FilesTab = lazy(() => import('./files').then((x) => ({ default: x.FilesTab })))
const ModsTab = lazy(() => import('./mods').then((x) => ({ default: x.ModsTab })))
const WorldsTab = lazy(() => import('./worlds').then((x) => ({ default: x.WorldsTab })))
const PlayersTab = lazy(() => import('./players').then((x) => ({ default: x.PlayersTab })))
const BackupsTab = lazy(() => import('./backups').then((x) => ({ default: x.BackupsTab })))
const SchedulesTab = lazy(() => import('./schedules').then((x) => ({ default: x.SchedulesTab })))
const StatsTab = lazy(() => import('./stats').then((x) => ({ default: x.StatsTab })))
const SettingsTab = lazy(() => import('./settings').then((x) => ({ default: x.SettingsTab })))

export interface ServerSearch {
  /** Console: which view (live is the default). */
  view?: 'files' | 'problems'
  /** A tab's own section (settings: version, properties, access; mods: browse, configs). */
  sub?: string
  /** Players: the player whose sheet is open. */
  player?: string
  /** Files: the folder shown, from the server's root ("/plugins"). */
  path?: string
  /** Files: the file open beside it ("/server.properties"). */
  open?: string
}

/** A server: its header, its tabs, and the tab that's open. */
export function ServerPage({
  id,
  tab: rawTab,
  search,
  setSearch,
}: {
  id: string
  tab?: string
  search: ServerSearch
  /** push adds a history entry, so Back returns to where this was. */
  setSearch: (s: ServerSearch, opts?: { push?: boolean }) => void
}) {
  const go = useGo()
  const q = useServer(id)
  const perms = useMyPerms(id)
  const { server: live, status: liveStatus, nodeName } = useFleetServer(id)
  const server = q.data
  const failed = !!server && (failedStatus(statusOf(server, liveStatus)) || !!live?.active_conflict)
  const problem = useStartupProblem(id, failed).data

  if (!server) {
    if (q.isError) {
      if (q.error instanceof ApiError && (q.error.status === 404 || q.error.status === 403)) {
        return (
          <div className="scroll">
            <div className="page">
              <Empty title="That server doesn’t exist any more, or isn’t shared with you." action={<Btn onClick={() => go(paths.home)}>Go home</Btn>} />
            </div>
          </div>
        )
      }
      return (
        <div className="scroll">
          <div className="page">
            <LoadError what="this server" failure={failureOf(q.error)} onRetry={() => void q.refetch()} />
          </div>
        </div>
      )
    }
    return <ServerSkeleton />
  }

  const my = perms.data
  const status = statusOf(server, liveStatus)
  const fallback: ServerTab = !my || allowed(my, 'console') ? 'console' : 'stats'
  const tab: ServerTab = isServerTab(rawTab) ? rawTab : fallback
  const def = SERVER_TABS.find((t) => t.value === tab) ?? SERVER_TABS[0]
  const detected = problem?.problem?.detected_at
  const crashedAt = detected ? (detected < 1e12 ? detected * 1000 : detected) : undefined

  let body
  if (my && !allowed(my, def.perm)) {
    body = (
      <div className="scroll">
        <div className="page">
          <Empty title={`You can’t open ${tabLabel(server.platform, tab)} on ${server.name}.`}>{deniedTip(def.perm)} An administrator can give it to you.</Empty>
        </div>
      </div>
    )
  } else if (tab === 'console') {
    body = (
      <ConsoleTab
        server={server}
        live={live}
        status={status}
        perms={my}
        nodeName={nodeName}
        view={(search.view as ConsoleView) ?? 'live'}
        setView={(v) => setSearch({ ...search, view: v === 'live' ? undefined : v })}
      />
    )
  } else if (tab === 'players') {
    body = (
      <TabPage>
        <PlayersTab server={server} status={status} perms={my} player={search.player} setPlayer={(player) => setSearch({ ...search, player })} />
      </TabPage>
    )
  } else if (tab === 'backups') {
    body = (
      <TabPage>
        <BackupsTab server={server} status={status} />
      </TabPage>
    )
  } else if (tab === 'settings') {
    body = (
      <TabPage>
        <SettingsTab
          server={server}
          live={live}
          status={status}
          perms={my}
          section={sectionOf(search.sub)}
          setSection={(sec) => setSearch({ ...search, sub: sec === 'general' ? undefined : sec })}
        />
      </TabPage>
    )
  } else if (tab === 'stats') {
    body = (
      <TabPage>
        <StatsTab server={server} />
      </TabPage>
    )
  } else if (tab === 'mods') {
    body = (
      <TabPage>
        <ModsTab server={server} status={status} perms={my} view={modsViewOf(search.sub)} setView={(v) => setSearch({ ...search, sub: v === 'installed' ? undefined : v })} />
      </TabPage>
    )
  } else if (tab === 'worlds') {
    body = (
      <TabPage>
        <WorldsTab server={server} status={status} />
      </TabPage>
    )
  } else if (tab === 'schedules') {
    body = (
      <TabPage>
        <SchedulesTab server={server} />
      </TabPage>
    )
  } else {
    body = (
      <FilesTab
        server={server}
        status={status}
        path={search.path || (search.open ? parentOf(search.open) : '/')}
        open={search.open}
        setLocation={(path, open) => setSearch({ ...search, path: path === '/' ? undefined : path, open }, { push: true })}
      />
    )
  }

  return (
    <PermScope perms={my ?? null}>
      <ServerHeader server={server} live={live} status={status} nodeName={nodeName} perms={my} tab={tab} crashedAt={crashedAt} />
      <Suspense fallback={<div className="scroll" aria-busy="true" />}>{body}</Suspense>
    </PermScope>
  )
}

function ServerSkeleton() {
  return (
    <div className="sk-fade" style={{ display: 'contents' }} aria-busy="true" aria-label="Loading">
      <header className="srv-head">
        <div className="srv-top">
          <Sk w={40} h={40} r={10} />
          <div className="grow stack" style={{ gap: 10 }}>
            <Sk w={220} h={22} r={8} />
            <Sk w={360} h={22} r={999} />
          </div>
          <Sk w={180} h={34} r={999} />
        </div>
        <Sk w={560} h={34} r={999} style={{ marginTop: 18 }} />
      </header>
      <ConsoleSkeleton />
    </div>
  )
}

/** The scrolling page under the header that most tabs sit in. */
function TabPage({ children }: { children: React.ReactNode }) {
  return (
    <div className="scroll">
      <div className="page" style={{ paddingTop: 22 }}>
        {children}
      </div>
    </div>
  )
}
