import { useEffect, useRef, useState } from 'react'
import { ServerMetrics, WsMessage } from '@/lib/ws'

interface Sample {
  cpu: number
  ram_used_mb: number
  ram_total_mb: number
}

// Shared sparkline dimensions. The placeholder and the rendered <svg> use the
// same height so the box never changes size between the empty state and the
// first metrics sample.
const SPARK_H = 28
const SPARK_W = 200

function Sparkline({ data, color }: { data: number[]; color: string }) {
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
  const min = lo - pad
  const max = hi + pad
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
}: {
  serverId: string
  // The server's configured max heap, so we can graph process RAM against the
  // limit it actually competes for rather than the (much larger) host total.
  ramMaxMb?: number
  // Live server status. When the server isn't running there's no process to
  // sample, so the agent stops emitting metrics — we must drop the accumulated
  // history rather than leave the last reading frozen on screen.
  status?: string
}) {
  const [samples, setSamples] = useState<Sample[]>([])
  const wsRef = useRef<ServerMetrics | null>(null)

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
  // instead of showing the last sample from when it was alive.
  useEffect(() => {
    if (!running) setSamples([])
  }, [running])

  useEffect(() => {
    const ws = new ServerMetrics(serverId)
    wsRef.current = ws

    const unsub = ws.on((msg: WsMessage) => {
      if (msg.type === 'metrics') {
        // Ignore any straggler sample that arrives after the server stopped.
        if (!runningRef.current) return
        const d = msg.data as {
          cpu_percent?: number
          ram_used_mb?: number
          ram_total_mb?: number
        }
        setSamples((prev) => {
          const next: Sample = {
            cpu: d.cpu_percent ?? 0,
            ram_used_mb: d.ram_used_mb ?? 0,
            ram_total_mb: d.ram_total_mb ?? 1,
          }
          return [...prev, next].slice(-60)
        })
      }
    })

    ws.connect()
    return () => {
      unsub()
      ws.disconnect()
    }
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

  return (
    <div className="grid grid-cols-2 gap-3">
      <div className="bg-surface rounded-lg border border-border p-3">
        <div className="flex items-center justify-between mb-1.5">
          <span className="text-xs font-medium text-text-secondary uppercase tracking-wide">CPU</span>
          <span className="text-sm font-mono font-medium text-text-primary">
            {latest ? `${latest.cpu.toFixed(1)}%` : '—'}
          </span>
        </div>
        <Sparkline data={cpuData} color="#22c55e" />
      </div>
      <div className="bg-surface rounded-lg border border-border p-3">
        <div className="flex items-center justify-between mb-1.5">
          <span className="text-xs font-medium text-text-secondary uppercase tracking-wide">RAM</span>
          {/* Single line so the box keeps a constant height before/after the
              first metrics sample arrives. Host total stays in the tooltip. */}
          <span
            className="text-sm font-mono font-medium text-text-primary"
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
    </div>
  )
}
