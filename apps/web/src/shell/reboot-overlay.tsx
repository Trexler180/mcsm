import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { Btn, Meter } from '@/ui/controls'
import { BrandMark, Icon } from '@/ui/icon'
import {
  nextPhase,
  useRebootTracker,
  type RebootObservation,
  type RebootPhase,
} from '@/lib/reboot-tracker'
import { useNotifications } from '@/store/notifications'

// Following a host reboot after "Reboot host" is accepted. While the panel
// still answers, a banner above the page says what's happening and the rest
// of the panel keeps working. Once the panel itself stops answering, it runs
// on the machine being rebooted, so nothing else on screen can work: the view
// takes the whole screen until it's back. Either way it keeps checking on its
// own, so nobody has to guess when to refresh.
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
  { phase: 'stopping', label: 'Stop its servers cleanly' },
  { phase: 'restarting', label: 'Restart the host' },
  { phase: 'back', label: 'The panel answers' },
]
const ORDER: Record<RebootPhase, number> = { stopping: 0, restarting: 1, back: 2 }

export function RebootOverlay() {
  const tracked = useRebootTracker((s) => s.tracked)
  const clear = useRebootTracker((s) => s.clear)
  const qc = useQueryClient()
  const { success } = useNotifications()

  const [phase, setPhase] = useState<RebootPhase>('stopping')
  const [reachable, setReachable] = useState(true)
  // Once the panel has stopped answering it stays a takeover until the end,
  // so a brief answer while services start doesn't flash the page back.
  const [panelDown, setPanelDown] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  const phaseRef = useRef<RebootPhase>('stopping')
  // The polling loop must not restart on every render (the clock below
  // re-renders each second), so the callbacks it uses are read through a ref
  // instead of being effect dependencies.
  const onBack = useRef(() => {})
  onBack.current = () => {
    if (!tracked) return
    success(`${tracked.nodeName} is back online`, 'The host restarted and the panel reconnected.')
    qc.invalidateQueries()
    clear()
  }

  // A new reboot starts from the beginning.
  useEffect(() => {
    phaseRef.current = 'stopping'
    setPhase('stopping')
    setReachable(true)
    setPanelDown(false)
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
      if (!obs.reachable) setPanelDown(true)
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
  const name = tracked.nodeName

  let warning: string | null = null
  if (phase === 'stopping' && elapsed > STOPPING_WARN_MS) {
    warning =
      "The host still hasn't gone down. If it doesn't, the reboot may have been refused after the servers stopped. Check the node, and start its servers again if needed."
  } else if (phase === 'restarting' && elapsed > RESTARTING_WARN_MS) {
    warning =
      'This is taking longer than a normal restart. The page keeps checking; if the host does not return, it may need attention from whoever manages the machine.'
  }

  if (!panelDown) {
    const text = phase === 'stopping' ? `Rebooting ${name}. Stopping its servers first.` : `Rebooting ${name}. Waiting for it to come back.`
    return (
      <div className="progress-banner" role="status">
        <span className="grow">
          {text}
          {warning && <span className="t2"> {warning}</span>}
        </span>
        <span className="track">
          <Meter busy value={0} max={1} label="Reboot progress" />
        </span>
        <span className="t3 num small" title="Time since the reboot was requested">
          {formatElapsed(elapsed)}
        </span>
        <Btn size="sm" variant="quiet" onClick={clear}>
          Stop waiting
        </Btn>
      </div>
    )
  }

  return createPortal(
    <div className="takeover" role="alertdialog" aria-modal="true" aria-labelledby="reboot-title">
      <div className="takeover-box">
        <BrandMark size={28} />
        <h1 id="reboot-title" className="takeover-title">
          {`Waiting for ${name} to come back`}
        </h1>
        <p className="t2" aria-live="polite">
          {reachable
            ? 'The host is restarting. This page reconnects on its own as soon as it is back.'
            : 'The panel runs on this machine too, so it’s away until the reboot finishes. This page checks every few seconds and comes back by itself. There’s no need to refresh.'}
        </p>
        <ol className="takeover-steps">
          {STEPS.map((step, i) => (
            <li key={step.phase} className={i < current ? 'done' : i === current ? 'now' : ''}>
              <span className="mark">
                {i < current ? <Icon name="check" size={13} label="done" /> : i === current ? <Icon name="refresh" size={13} className="spin" label="in progress" /> : null}
              </span>
              {step.label}
            </li>
          ))}
        </ol>
        <div className="row" style={{ justifyContent: 'space-between' }}>
          <span className="t3 small num" title="Time since the reboot was requested">
            {formatElapsed(elapsed)} so far
          </span>
          <Btn size="sm" variant="quiet" onClick={clear}>
            Stop waiting
          </Btn>
        </div>
        {warning && <p className="small t2 takeover-slow">{warning}</p>}
      </div>
    </div>,
    document.body,
  )
}
