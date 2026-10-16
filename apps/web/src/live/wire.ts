import type { QueryClient } from '@tanstack/react-query'
import { deliverNotification, notificationFromEvent } from '@/lib/notify-stream'
import type { FleetSummary, Node, Server } from '@/lib/types'
import { fmtDur } from '@/ui/format'
import { toast } from '@/ui/toasts'
import { applyToFleet, applyToNode, applyToServer, type LiveEvent } from './apply'
import type { LiveHandlers } from './events'

// Where each live event lands: the cached API responses that screens render
// from, the alert feed, or a refetch when the event says something changed
// that it doesn't carry.

export function liveHandlers(qc: QueryClient): LiveHandlers {
  return {
    onEvent: (e) => routeEvent(qc, e),
    onReset: () => {
      void qc.invalidateQueries()
    },
    onBack: (gapMs) => {
      // What was missed has been replayed (or refetched, after a reset); pages
      // that failed while the panel was away load again now that it's back.
      void qc.refetchQueries({ type: 'active', predicate: (q) => q.state.status === 'error' })
      toast('ok', 'Live updates are back', gapMs >= 1000 ? `Caught up on ${fmtDur(gapMs, 1)} of activity.` : undefined)
    },
  }
}

// What an event changes beyond what it carries (the activity list, backup
// times) arrives with the next fleet fetch, made once things settle rather
// than once per event.
const SETTLE_MS = 1500
const settleTimers = new WeakMap<QueryClient, ReturnType<typeof setTimeout>>()

function refreshFleetSoon(qc: QueryClient) {
  clearTimeout(settleTimers.get(qc))
  settleTimers.set(
    qc,
    setTimeout(() => void qc.invalidateQueries({ queryKey: ['fleet'] }), SETTLE_MS),
  )
}

export function routeEvent(qc: QueryClient, e: LiveEvent) {
  switch (e.type) {
    case 'notification': {
      const item = notificationFromEvent(e.data)
      if (item) deliverNotification(item)
      return
    }
    case 'server.status':
      qc.setQueryData<FleetSummary>(['fleet'], (f) => f && applyToFleet(f, e))
      qc.setQueryData<Server[]>(['servers'], (list) => list?.map((s) => applyToServer(s, e)))
      if (e.server_id) qc.setQueryData<Server>(['server', e.server_id], (s) => s && applyToServer(s, e))
      void qc.invalidateQueries({ queryKey: ['overview'] })
      refreshFleetSoon(qc)
      return
    case 'server.stats':
      qc.setQueryData<FleetSummary>(['fleet'], (f) => f && applyToFleet(f, e))
      return
    case 'server.players':
      qc.setQueryData<FleetSummary>(['fleet'], (f) => f && applyToFleet(f, e))
      if (e.server_id) void qc.invalidateQueries({ queryKey: ['players', e.server_id] })
      return
    case 'operation.updated':
      qc.setQueryData<FleetSummary>(['fleet'], (f) => f && applyToFleet(f, e))
      if (e.data?.status !== 'running') refreshFleetSoon(qc)
      return
    case 'node.status':
      qc.setQueryData<FleetSummary>(['fleet'], (f) => f && applyToFleet(f, e))
      qc.setQueryData<Node[]>(['nodes'], (list) => list?.map((n) => applyToNode(n, e)))
      return
    case 'server.updated':
      void qc.invalidateQueries({ queryKey: ['fleet'] })
      void qc.invalidateQueries({ queryKey: ['servers'] })
      if (e.server_id) {
        void qc.invalidateQueries({ queryKey: ['server', e.server_id] })
        if (e.data?.what === 'mods') void qc.invalidateQueries({ queryKey: ['mods', e.server_id] })
      }
      return
    case 'server.deleted':
      qc.setQueryData<FleetSummary>(['fleet'], (f) => f && applyToFleet(f, e))
      qc.setQueryData<Server[]>(['servers'], (list) => list?.filter((s) => s.id !== e.server_id))
      return
    case 'task.run':
      if (e.server_id) void qc.invalidateQueries({ queryKey: ['tasks', e.server_id] })
      refreshFleetSoon(qc)
      return
  }
}
