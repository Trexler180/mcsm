import { useEffect, useRef, useState } from 'react'
import { ServerMetrics, WsMessage } from '@/lib/ws'

interface Sample {
  cpu: number
  ram_used_mb: number
  ram_total_mb: number
}

function Sparkline({ data, color }: { data: number[]; color: string }) {
  if (data.length < 2) {
    return <div className="h-10 w-full" />
  }
  const h = 40
  const w = 200
  const max = 100
  const pts = data
    .map((v, i) => {
      const x = (i / (data.length - 1)) * w
      const y = h - Math.max(0, Math.min(v, max) / max) * h
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')

  return (
    <svg
      width="100%"
      height={h}
      viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none"
      className="overflow-visible"
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

export function ResourceChart({ serverId }: { serverId: string }) {
  const [samples, setSamples] = useState<Sample[]>([])
  const wsRef = useRef<ServerMetrics | null>(null)

  useEffect(() => {
    const ws = new ServerMetrics(serverId)
    wsRef.current = ws

    const unsub = ws.on((msg: WsMessage) => {
      if (msg.type === 'metrics') {
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
  const ramData = samples.map((s) =>
    s.ram_total_mb > 0 ? (s.ram_used_mb / s.ram_total_mb) * 100 : 0,
  )

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
          <span className="text-sm font-mono font-medium text-text-primary">
            {latest
              ? `${latest.ram_used_mb} / ${latest.ram_total_mb} MB`
              : '—'}
          </span>
        </div>
        <Sparkline data={ramData} color="#3b82f6" />
      </div>
    </div>
  )
}
