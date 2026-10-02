import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2, Loader2, Power } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import {
  nextPhase,
  useRebootTracker,
  type RebootObservation,
  type RebootPhase,
} from '@/lib/reboot-tracker'
import { useNotifications } from '@/store/notifications'

// The full-screen view shown after "Reboot host" is accepted. It takes over the
// app because, when the dashboard runs on the machine being rebooted, nothing
// else on screen can work until it is back — and it keeps checking on its own,
// so nobody has to guess when to refresh.
//
// Imported statically by the root layout: while the dashboard's host is down,
// a lazily-loaded chunk could not be fetched.

export const POLL_INTERVAL_MS = 3_000
const PROBE_TIMEOUT_MS = 5_000
/** Still not down after this long: the reboot may have been refused late. */
const STOPPING_WARN_MS = 5 * 60_000
/** Still not back after this long: something needs a human. */
const RESTARTING_WARN_MS = 10 * 60_000

async function observe(nodeId: string): Promise<RebootObservation> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), PROBE_TIMEOUT_MS)
  try {
    const nodes = await api.nodes.list(controller.signal)
    return { reachable: true, node: nodes.find((n) => n.id === nodeId) ?? null }
  } catch {
    // A network error, a timeout, or the proxy's 502 while the API is down all
    // mean the same thing here: the dashboard cannot be reached yet.
    return { reachable: false, node: null }
  } finally {
    clearTimeout(timer)
  }
}

function formatElapsed(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${m}:${String(s).padStart(2, '0')}`
}

const STEPS: { phase: RebootPhase; label: string }[] = [
  { phase: 'stopping', label: 'Stopping servers' },
  { phase: 'restarting', label: 'Restarting the host' },
  { phase: 'back', label: 'Reconnecting' },
]
const ORDER: Record<RebootPhase, number> = { stopping: 0, restarting: 1, back: 2 }

export function RebootOverlay() {
  const tracked = useRebootTracker((s) => s.tracked)
  const clear = useRebootTracker((s) => s.clear)
  const qc = useQueryClient()
  const { success } = useNotifications()

  const [phase, setPhase] = useState<RebootPhase>('stopping')
  const [reachable, setReachable] = useState(true)
  const [now, setNow] = useState(() => Date.now())
  const phaseRef = useRef<RebootPhase>('stopping')
  // The polling loop must not restart on every render (the clock below
  // re-renders each second), so the callbacks it uses are read through a ref
  // instead of being effect dependencies.
  const onBack = useRef(() => {})
  onBack.current = () => {
    if (!tracked) return
    success(`${tracked.nodeName} is back online`, 'The host restarted and the dashboard reconnected.')
    qc.invalidateQueries()
    clear()
  }

  // A new reboot starts from the beginning.
  useEffect(() => {
    phaseRef.current = 'stopping'
    setPhase('stopping')
    setReachable(true)
  }, [tracked?.nodeId, tracked?.requestedAt])

  // One probe at a time: the next is scheduled only after the previous one
  // settles, so a host that is slow to refuse connections never piles them up.
  useEffect(() => {
    if (!tracked) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined

    const tick = async () => {
      const obs = await observe(tracked.nodeId)
      if (cancelled) return
      setReachable(obs.reachable)
      const next = nextPhase(phaseRef.current, obs, tracked.requestedAt, tracked.previousBoot)
      phaseRef.current = next
      setPhase(next)
      if (next === 'back') {
        onBack.current()
        return
      }
      timer = setTimeout(tick, POLL_INTERVAL_MS)
    }
    timer = setTimeout(tick, POLL_INTERVAL_MS)
    return () => {
      cancelled = true
      if (timer) clearTimeout(timer)
    }
  }, [tracked])

  // Elapsed-time display.
  useEffect(() => {
    if (!tracked) return
    const id = setInterval(() => setNow(Date.now()), 1_000)
    return () => clearInterval(id)
  }, [tracked])

  if (!tracked) return null

  const elapsed = now - tracked.startedAt
  const current = ORDER[phase]

  let title: string
  let detail: string
  if (phase === 'stopping') {
    title = `Rebooting ${tracked.nodeName}`
    detail = 'Each server is saved and shut down before the host restarts. This can take up to a minute per server.'
  } else {
    title = `Waiting for ${tracked.nodeName} to come back`
    detail = reachable
      ? 'The host is restarting. This page reconnects on its own as soon as it is back.'
      : 'The dashboard runs on this machine, so it is offline too. This page reconnects on its own as soon as it is back.'
  }

  let warning: string | null = null
  if (phase === 'stopping' && elapsed > STOPPING_WARN_MS) {
    warning =
      "The host still hasn't gone down. If it doesn't, the reboot may have been refused after the servers stopped — check the node, and start its servers again if needed."
  } else if (phase === 'restarting' && elapsed > RESTARTING_WARN_MS) {
    warning =
      'This is taking longer than a normal restart. The page keeps checking; if the host does not return, it may need attention from whoever manages the machine.'
  }

  return (
    <div
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="reboot-title"
      className="fixed inset-0 z-[100] flex items-center justify-center bg-background/95 p-4 backdrop-blur-sm"
    >
      <div className="w-full max-w-md space-y-6 rounded-lg border border-border bg-surface p-6 shadow-xl">
        <div className="flex items-start gap-3">
          <div className="rounded-full bg-amber-500/10 p-2.5 text-amber-400">
            <Power className="h-5 w-5" />
          </div>
          <div className="min-w-0 space-y-1">
            <h2 id="reboot-title" className="font-medium text-text-primary">
              {title}
            </h2>
            <p className="text-sm text-text-secondary" aria-live="polite">
              {detail}
            </p>
          </div>
        </div>

        <ol className="space-y-2.5">
          {STEPS.map((step, i) => {
            const done = i < current
            const active = i === current
            return (
              <li
                key={step.phase}
                className={`flex items-center gap-2.5 text-sm ${
                  done || active ? 'text-text-primary' : 'text-text-secondary/60'
                }`}
              >
                {done ? (
                  <CheckCircle2 className="h-4 w-4 text-green-400" aria-label="done" />
                ) : active ? (
                  <Loader2 className="h-4 w-4 animate-spin text-accent" aria-label="in progress" />
                ) : (
                  <span className="h-4 w-4 rounded-full border border-border" aria-hidden />
                )}
                {step.label}
              </li>
            )
          })}
        </ol>

        {warning && (
          <div className="flex gap-2.5 rounded-md border border-amber-900/60 bg-amber-950/30 p-3 text-sm text-text-secondary">
            <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-amber-400" />
            <p>{warning}</p>
          </div>
        )}

        <div className="flex items-center justify-between gap-3 border-t border-border/50 pt-4">
          <span className="font-mono text-xs text-text-secondary" title="Time since the reboot was requested">
            {formatElapsed(elapsed)}
          </span>
          <Button variant="outline" size="sm" onClick={clear}>
            Stop waiting
          </Button>
        </div>
      </div>
    </div>
  )
}
