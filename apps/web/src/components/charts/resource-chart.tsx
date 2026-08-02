import { useEffect, useRef, useState } from 'react'
import { subscribeServerMetrics, WsMessage } from '@/lib/ws'
import { C_TPS } from './colors'

interface Sample {
  cpu: number
  ram_used_mb: number
  ram_total_mb: number
}

// One point per snapshot the helper mod reported, not per metrics frame — see
// appendTickSample.
export interface TickSample {
  seq: number
  tps: number
}

// The tick fields the agent adds to a metrics frame when the helper mod is
// linked and reporting. Absent on every other server, which is what keeps the
// TPS card invisible rather than empty.
type TickFrame = { tps?: number; tick_seq?: number }

// TPS is graphed against a fixed 0-20 rather than auto-scaled like CPU and RAM.
// Auto-scaling is right for those — they genuinely roam — and actively wrong
// here: a healthy server sits at 20.00 with a few hundredths of jitter, and
// scaling to its own range would zoom that jitter until a perfectly fine server
// looked like it was thrashing. Pinned to the ceiling, healthy reads as a flat
// line along the top and a real dip is unmistakable.
const TPS_DOMAIN: [number, number] = [0, 20]

// How many points each series keeps. At the mod's 15s heartbeat, 60 tick points
// is a quarter hour of history; at the 2s metrics cadence the same count is two
// minutes of CPU and RAM.
const MAX_SAMPLES = 60

/**
 * Folds one metrics frame into the tick series.
 *
 * The metrics stream ticks every 2s but the mod only reports every 15s, so most
 * frames restate a reading already plotted. Appending them would draw a
 * staircase claiming a resolution the data does not have, so a point is added
 * only when the agent's snapshot counter has actually moved.
 *
 * A frame with no tick data clears the series: the agent omits those fields
 * once the mod's last snapshot goes stale, and a frozen line is a worse answer
 * than no line at all.
 */
export function appendTickSample(
  prev: TickSample[],
  frame: TickFrame,
): TickSample[] {
  // Returning prev unchanged wherever nothing was added matters: React bails
  // out of a re-render on an identical state reference, and the common cases
  // here — a repeated reading, or a server with no mod at all — recur every 2s
  // for the life of the page.
  if (frame.tps == null || frame.tick_seq == null) {
    return prev.length === 0 ? prev : []
  }
  if (prev.length > 0 && prev[prev.length - 1].seq === frame.tick_seq) {
    return prev
  }
  const next = prev.length === MAX_SAMPLES ? prev.slice(1) : prev.slice()
  next.push({ seq: frame.tick_seq, tps: frame.tps })
  return next
}

// Shared sparkline dimensions. The placeholder and the rendered <svg> use the
// same height so the box never changes size between the empty state and the
// first metrics sample.
const SPARK_H = 28
const SPARK_W = 200

function Sparkline({
  data,
  color,
  domain,
}: {
  data: number[]
  color: string
  // Fixed [min, max] to plot against. Omit to auto-scale to the data's own
  // range, which is what CPU and RAM want.
  domain?: [number, number]
}) {
  const h = SPARK_H
  const w = SPARK_W
  if (data.length < 2) {
    return <div style={{ height: h }} className="w-full" />
  }
  // Auto-scale to the data's own range so small movements (a few MB of RAM,
  // a few % CPU) are visible instead of pinned flat against a 0-100 axis.
  // Pad by 10% and enforce a minimum span so a steady signal sits mid-height.
  const lo = Math.min(...data)
  const hi = Math.max(...data)
  const span = Math.max(hi - lo, 1)
  const pad = span * 0.1
  const min = domain ? domain[0] : lo - pad
  const max = domain ? domain[1] : hi + pad
  const pts = data
    .map((v, i) => {
      const x = (i / (data.length - 1)) * w
      const norm = (v - min) / (max - min)
      const y = h - Math.max(0, Math.min(norm, 1)) * h
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')

  return (
    <svg
      width="100%"
      height={h}
      viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none"
      className="block overflow-visible"
    >
      <polyline
        points={pts}
        fill="none"
        stroke={color}
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  )
}

export function ResourceChart({
  serverId,
  ramMaxMb,
  status,
  showTps = false,
}: {
  serverId: string
  // The server's configured max heap, so we can graph process RAM against the
  // limit it actually competes for rather than the (much larger) host total.
  ramMaxMb?: number
  // Live server status. When the server isn't running there's no process to
  // sample, so the agent stops emitting metrics — we must drop the accumulated
  // history rather than leave the last reading frozen on screen.
  status?: string
  // Whether to offer the tick-rate card at all. Off by default because this
  // also renders in the page header at w-64, where a third column would crush
  // all three cards; the dashboard has the room and opts in.
  showTps?: boolean
}) {
  const [samples, setSamples] = useState<Sample[]>([])
  const [ticks, setTicks] = useState<TickSample[]>([])

  // A process only exists (and thus only produces metrics) while starting or
  // online. Suppress stale data only when we positively know the server is in a
  // non-running state; an unknown/undefined status falls back to showing live
  // samples so a caller that omits the prop never blanks the graph.
  const running =
    status === undefined ||
    status === 'online' ||
    status === 'starting' ||
    status === 'restarting'
  const runningRef = useRef(running)
  runningRef.current = running

  // Clear history the moment the server stops, so CPU/RAM fall back to "—"
  // instead of showing the last sample from when it was alive. Tick history goes
  // with it: a stopped server has no tick rate, and the card should leave rather
  // than sit there holding the number it had when the server died.
  useEffect(() => {
    if (!running) {
      setSamples([])
      setTicks([])
    }
  }, [running])

  useEffect(() => {
    return subscribeServerMetrics(serverId, (msg: WsMessage) => {
      if (msg.type === 'metrics') {
        // Ignore any straggler sample that arrives after the server stopped.
        if (!runningRef.current) return
        const d = msg.data as {
          cpu_percent?: number
          ram_used_mb?: number
          ram_total_mb?: number
        } & TickFrame
        setSamples((prev) => {
          const next: Sample = {
            cpu: d.cpu_percent ?? 0,
            ram_used_mb: d.ram_used_mb ?? 0,
            ram_total_mb: d.ram_total_mb ?? 1,
          }
          const samples = prev.length === MAX_SAMPLES ? prev.slice(1) : prev.slice()
          samples.push(next)
          return samples
        })
        setTicks((prev) => appendTickSample(prev, d))
      }
    })
  }, [serverId])

  const latest = samples[samples.length - 1]
  const cpuData = samples.map((s) => s.cpu)
  // Graph process RAM as a fraction of the server's configured heap when we know
  // it; otherwise fall back to the host total. ram_used_mb is per-process RSS,
  // ram_total_mb is host memory — graphing one against the other understates the
  // server's actual memory pressure.
  const ramData = samples.map((s) => {
    const denom = ramMaxMb && ramMaxMb > 0 ? ramMaxMb : s.ram_total_mb
    return denom > 0 ? (s.ram_used_mb / denom) * 100 : 0
  })

  // The card is offered only where there is room and only once the mod has
  // actually reported. A vanilla server, or one whose mod is not linked, keeps
  // exactly the two-card layout it has always had.
  const tpsCard = showTps && ticks.length > 0
  const latestTps = ticks[ticks.length - 1]

  return (
    // Stack the cards on very narrow viewports so the RAM readout
    // ("1024 / 4096 MB") never gets squeezed; side-by-side from sm up, and a
    // third column only when there is a tick card to put in it.
    <div
      className={`grid grid-cols-1 gap-3 sm:grid-cols-2${
        tpsCard ? ' lg:grid-cols-3' : ''
      }`}
    >
      <div className="min-w-0 bg-surface rounded-lg border border-border p-3">
        <div className="flex items-center justify-between gap-2 mb-1.5">
          <span className="text-xs font-medium text-text-secondary uppercase tracking-wide">CPU</span>
          <span className="text-sm font-mono font-medium text-text-primary">
            {latest ? `${latest.cpu.toFixed(1)}%` : '—'}
          </span>
        </div>
        <Sparkline data={cpuData} color="#22c55e" />
      </div>
      <div className="min-w-0 bg-surface rounded-lg border border-border p-3">
        <div className="flex items-center justify-between gap-2 mb-1.5">
          <span className="text-xs font-medium text-text-secondary uppercase tracking-wide">RAM</span>
          {/* Single line so the box keeps a constant height before/after the
              first metrics sample arrives. Host total stays in the tooltip. */}
          <span
            className="truncate text-sm font-mono font-medium text-text-primary"
            title={latest ? `Host total: ${latest.ram_total_mb} MB` : undefined}
          >
            {latest
              ? `${latest.ram_used_mb} / ${
                  ramMaxMb && ramMaxMb > 0 ? ramMaxMb : latest.ram_total_mb
                } MB`
              : '—'}
          </span>
        </div>
        <Sparkline data={ramData} color="#3b82f6" />
      </div>
      {tpsCard && (
        <div className="min-w-0 bg-surface rounded-lg border border-border p-3">
          <div className="flex items-center justify-between gap-2 mb-1.5">
            <span className="text-xs font-medium text-text-secondary uppercase tracking-wide">TPS</span>
            {/* Capped at 20: a rolling average can float a hair over the tick
                ceiling, and "20.1" just makes the number look broken. */}
            <span
              className="text-sm font-mono font-medium text-text-primary"
              title="Tick rate reported by the helper mod, updated every 15s"
            >
              {Math.min(latestTps.tps, 20).toFixed(2)}
            </span>
          </div>
          <Sparkline
            data={ticks.map((t) => t.tps)}
            color={C_TPS}
            domain={TPS_DOMAIN}
          />
        </div>
      )}
    </div>
  )
}
