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
import { LegacyTab } from './legacy'
import { PlayersTab } from './players'
import { BackupsTab } from './backups'
import { SchedulesTab } from './schedules'
import { SERVER_TABS, isServerTab, type ServerTab } from './tabs'

export interface ServerSearch {
  /** Console: which view (live is the default). */
  view?: 'files' | 'problems'
  /** A tab's own section (settings: version, properties, access; mods: configs). */
  sub?: string
  /** Players: the player whose sheet is open. */
  player?: string
}

/** A server: its header, its tabs, and the tab that's open. */
export function ServerPage({ id, tab: rawTab, search, setSearch }: { id: string; tab?: string; search: ServerSearch; setSearch: (s: ServerSearch) => void }) {
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
  } else if (tab === 'schedules') {
    body = (
      <TabPage>
        <SchedulesTab server={server} />
      </TabPage>
    )
  } else {
    body = <LegacyTab key={tab} tab={tab} sub={search.sub} setSub={(sub) => setSearch({ ...search, sub })} server={server} perms={my} />
  }

  return (
    <PermScope perms={my ?? null}>
      <ServerHeader server={server} live={live} status={status} nodeName={nodeName} perms={my} tab={tab} crashedAt={crashedAt} />
      {body}
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
