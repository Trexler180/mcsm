import { Fragment, useLayoutEffect, useMemo, useRef, useState, type RefObject } from 'react'
import { DOW, DOW_FULL, fmtClock, fmtDate, fmtNum } from './format'
import { clamp, pad2 } from './util'

// Charts: one axis, thin lines, a quiet grid, and a crosshair that finds the
// nearest point. Drawn as SVG so they follow the theme's colors.

function useWidth(ref: RefObject<HTMLElement | null>) {
  const [w, setW] = useState(0)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    setW(el.getBoundingClientRect().width)
    const ro = new ResizeObserver(([e]) => setW(e.contentRect.width))
    ro.observe(el)
    return () => ro.disconnect()
  }, [ref])
  return w
}

/** Round axis values from min to past max. `integer` keeps them whole, for
 *  counts (a player is never 0.5). */
export function niceTicks(min: number, max: number, count = 4, integer = false): number[] {
  const span = max - min || 1
  const step0 = span / count
  const mag = Math.pow(10, Math.floor(Math.log10(step0)))
  const err = step0 / mag
  const raw = (err >= 7.5 ? 10 : err >= 3.5 ? 5 : err >= 1.5 ? 2 : 1) * mag
  const step = integer ? Math.max(1, Math.round(raw)) : raw
  const lo = Math.floor(min / step) * step
  const hi = Math.ceil(max / step) * step
  const out: number[] = []
  for (let v = lo; v <= hi + 1e-9; v += step) out.push(+v.toFixed(6))
  return out
}

let gradientSeq = 0

export interface Point {
  ts: number
  [k: string]: number
}

/** A line over time. k2 adds a second, recessed line for the same
 *  measurement at another level (a p95 next to the median). */
export function LineChart({
  data,
  k,
  k2,
  kLabel,
  k2Label,
  height = 160,
  yMin = 0,
  yMax,
  yFmt = (v) => fmtNum(v),
  xFmt = (t) => fmtClock(t),
  refLine,
  label,
  tipTitle,
  color = 'var(--chart)',
  integer,
}: {
  data: Point[]
  k: string
  k2?: string
  kLabel?: string
  k2Label?: string
  height?: number
  yMin?: number
  yMax?: number
  yFmt?: (v: number) => string
  xFmt?: (t: number) => string
  refLine?: { v: number }
  label?: string
  tipTitle?: (p: Point) => string
  color?: string
  /** A count: whole numbers on the axis. */
  integer?: boolean
}) {
  const box = useRef<HTMLDivElement>(null)
  const w = useWidth(box)
  const [hi, setHi] = useState<number | null>(null)
  const gid = useMemo(() => 'chart-g' + ++gradientSeq, [])
  const pad = { l: 40, r: 8, t: 8, b: 22 }
  if (!data || data.length < 2) {
    return (
      <div ref={box} className="chart t3 small" style={{ height, display: 'grid', placeItems: 'center' }}>
        Nothing recorded yet
      </div>
    )
  }
  const iw = Math.max(10, w - pad.l - pad.r)
  const ih = height - pad.t - pad.b
  const t0 = data[0].ts
  const t1 = data[data.length - 1].ts
  const max = yMax != null ? yMax : Math.max(...data.map((d) => Math.max(d[k], k2 ? d[k2] : 0)), refLine ? refLine.v : 0) * 1.12 || 1
  const ticks = niceTicks(yMin, max, 3, integer)
  const top = yMax != null ? yMax : ticks[ticks.length - 1]
  const X = (ts: number) => pad.l + ((ts - t0) / (t1 - t0 || 1)) * iw
  const Y = (v: number) => pad.t + ih - ((v - yMin) / (top - yMin || 1)) * ih
  const line = (key: string) => data.map((d, i) => (i ? 'L' : 'M') + X(d.ts).toFixed(1) + ' ' + Y(d[key]).toFixed(1)).join(' ')
  const path = line(k)
  const path2 = k2 ? line(k2) : null
  const xt = [0, 0.5, 1].map((f) => t0 + (t1 - t0) * f)
  const onMove = (e: React.PointerEvent<SVGRectElement>) => {
    const r = e.currentTarget.getBoundingClientRect()
    // The hit area starts at pad.l, so the pointer is measured from its own left edge.
    const ts = t0 + ((e.clientX - r.left) / iw) * (t1 - t0)
    let best = 0
    let bd = Infinity
    data.forEach((d, i) => {
      const dd = Math.abs(d.ts - ts)
      if (dd < bd) {
        bd = dd
        best = i
      }
    })
    setHi(best)
  }
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowRight') setHi((h) => Math.min(data.length - 1, (h == null ? data.length - 2 : h) + 1))
    if (e.key === 'ArrowLeft') setHi((h) => Math.max(0, (h == null ? data.length - 1 : h) - 1))
  }
  const hd = hi != null ? data[hi] : null
  return (
    <div ref={box} className="chart" style={{ height }}>
      {w > 0 && (
        <svg width={w} height={height} role="img" aria-label={label} tabIndex={0} onKeyDown={onKey} onBlur={() => setHi(null)}>
          <g className="axis">
            {ticks.map((t) => (
              <g key={t}>
                <line className="grid-line" x1={pad.l} x2={pad.l + iw} y1={Y(t)} y2={Y(t)} />
                <text x={pad.l - 8} y={Y(t) + 4} textAnchor="end">
                  {yFmt(t)}
                </text>
              </g>
            ))}
            {xt.map((t, i) => (
              <text key={i} x={X(t)} y={height - 5} textAnchor={i === 0 ? 'start' : i === 2 ? 'end' : 'middle'}>
                {xFmt(t)}
              </text>
            ))}
          </g>
          {refLine && <line x1={pad.l} x2={pad.l + iw} y1={Y(refLine.v)} y2={Y(refLine.v)} stroke="var(--bad)" strokeOpacity=".5" strokeWidth="1" />}
          <defs>
            <linearGradient id={gid} x1="0" x2="0" y1="0" y2="1">
              <stop offset="0" stopColor="var(--chart)" stopOpacity=".18" />
              <stop offset="1" stopColor="var(--chart)" stopOpacity="0" />
            </linearGradient>
          </defs>
          <path d={path + ` L${X(t1)} ${Y(yMin)} L${X(t0)} ${Y(yMin)} Z`} fill={`url(#${gid})`} />
          {path2 && <path d={path2} fill="none" stroke={color} strokeOpacity=".42" strokeWidth="1" strokeLinejoin="round" strokeLinecap="round" />}
          <path d={path} fill="none" stroke={color} strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round" />
          {hd && <line x1={X(hd.ts)} x2={X(hd.ts)} y1={pad.t} y2={pad.t + ih} stroke="var(--text-3)" strokeWidth="1" />}
          {hd && k2 && <circle cx={X(hd.ts)} cy={Y(hd[k2])} r="3" fill="var(--canvas)" stroke={color} strokeOpacity=".6" strokeWidth="1.5" />}
          {hd && <circle cx={X(hd.ts)} cy={Y(hd[k])} r="4" fill={color} stroke="var(--canvas)" strokeWidth="2" />}
          <rect x={pad.l} y={0} width={iw} height={height} fill="transparent" onPointerMove={onMove} onPointerLeave={() => setHi(null)} />
        </svg>
      )}
      {hd && (
        <div className="chart-tip" style={{ left: clamp(X(hd.ts), 60, w - 60), top: Y(k2 ? hd[k2] : hd[k]) }}>
          {k2 ? (
            <>
              <span className="v">{yFmt(hd[k])}</span> <span className="l">{kLabel}</span>{' '}
              <span className="v" style={{ marginLeft: 8 }}>
                {yFmt(hd[k2])}
              </span>{' '}
              <span className="l">
                {k2Label}, {tipTitle ? tipTitle(hd) : xFmt(hd.ts)}
              </span>
            </>
          ) : (
            <>
              <span className="v">{yFmt(hd[k])}</span> <span className="l">{tipTitle ? tipTitle(hd) : xFmt(hd.ts)}</span>
            </>
          )}
        </div>
      )}
    </div>
  )
}

export interface Column {
  v: number
  label: string
}

export interface PeakBar extends Column {
  ts: number
  /** Bucket width in ms. */
  span: number
}

const HOUR = 3_600_000
const BAR_SPANS = [10, 15, 30, 60, 120, 180, 360, 720, 1440, 2880, 10080, 43200].map((m) => m * 60_000)
const LABEL_STEPS = [1, 3, 6, 12, 24, 48, 168, 336, 720].map((h) => h * HOUR)
const tzMs = (ts: number) => new Date(ts).getTimezoneOffset() * 60_000

/** Folds a time series into at most maxBars columns of the highest value in
 *  each, on local-time boundaries (whole hours, midnights). minSpan is the
 *  data's own resolution: narrower bars would only add empty slots. Empty
 *  stretches stay in as zero bars so the columns keep their place in time. */
export function peakBars(data: Point[], k: string, maxBars = 200, minSpan = 0): PeakBar[] {
  if (!data.length) return []
  const t0 = data[0].ts
  const t1 = data[data.length - 1].ts
  const span = BAR_SPANS.find((s) => s >= minSpan && (t1 - t0) / s < maxBars) ?? BAR_SPANS[BAR_SPANS.length - 1]
  const key = (ts: number) => Math.floor((ts - tzMs(ts)) / span)
  const first = key(t0)
  const out: PeakBar[] = []
  for (let i = first; i <= key(t1); i++) {
    const ts = i * span + tzMs(i * span)
    out.push({ ts, span, v: 0, label: '' })
  }
  for (const d of data) {
    const b = out[key(d.ts) - first]
    if (b) b.v = Math.max(b.v, d[k])
  }
  return out
}

/** Which of peakBars' columns get an axis label: those starting on a round
 *  local time (3 h, midnight, week…) chosen so at most maxLabels show. */
export function barTicks(bars: PeakBar[], maxLabels = 8): (b: PeakBar) => boolean {
  const span = bars[0]?.span ?? HOUR
  const total = bars.length * span
  const step = LABEL_STEPS.find((s) => s >= span && s % span === 0 && total / s <= maxLabels) ?? LABEL_STEPS[LABEL_STEPS.length - 1]
  return (b) => (b.ts - tzMs(b.ts)) % step === 0
}

/** One outline for gapless columns: a staircase over the bar tops from x0 in
 *  steps of band, closed along base, with every bend rounded by up to r. */
export function stepPath(tops: number[], x0: number, band: number, base: number, r = 3): string {
  if (!tops.length) return ''
  const pts: [number, number][] = [[x0, base]]
  tops.forEach((y, i) => pts.push([x0 + band * i, y], [x0 + band * (i + 1), y]))
  pts.push([x0 + band * tops.length, base])
  const v = pts.filter(([x, y], i) => {
    const p = pts[i - 1]
    const n = pts[i + 1]
    if (p && p[0] === x && p[1] === y) return false
    return !(p && n && ((p[0] === x && n[0] === x) || (p[1] === y && n[1] === y)))
  })
  let d = `M${v[0][0]} ${v[0][1]}`
  for (let i = 1; i < v.length - 1; i++) {
    const [px, py] = v[i - 1]
    const [x, y] = v[i]
    const [nx, ny] = v[i + 1]
    const a = Math.min(r, Math.hypot(x - px, y - py) / 2)
    const b = Math.min(r, Math.hypot(nx - x, ny - y) / 2)
    const ux = Math.sign(x - px), uy = Math.sign(y - py)
    const wx = Math.sign(nx - x), wy = Math.sign(ny - y)
    d += ` L${x - ux * a} ${y - uy * a} Q${x} ${y} ${x + wx * b} ${y + wy * b}`
  }
  const [lx, ly] = v[v.length - 1]
  return d + ` L${lx} ${ly} Z`
}

export function Columns<C extends Column>({
  data,
  height = 160,
  yFmt = fmtNum,
  xFmt,
  label,
  tip,
  joined,
  tick,
}: {
  data: C[]
  height?: number
  yFmt?: (v: number) => string
  xFmt?: (c: C) => string
  label?: string
  tip?: (c: C) => string
  /** Draw the columns edge to edge as one shape, for bars that are slices of
   *  a continuous series rather than separate counts. */
  joined?: boolean
  /** Which columns get an x label; by default an even spread of about 8. */
  tick?: (c: C) => boolean
}) {
  const box = useRef<HTMLDivElement>(null)
  const w = useWidth(box)
  const [hi, setHi] = useState<number | null>(null)
  const pad = { l: 34, r: 4, t: 8, b: 22 }
  const iw = Math.max(10, w - pad.l - pad.r)
  const ih = height - pad.t - pad.b
  const max = Math.max(1, ...data.map((d) => d.v))
  const ticks = niceTicks(0, max, 3, true)
  const top = ticks[ticks.length - 1]
  const band = iw / Math.max(1, data.length)
  const bw = joined ? band : Math.min(22, Math.max(3, band - 3))
  const Y = (v: number) => pad.t + ih - (v / top) * ih
  const every = Math.ceil(data.length / 8)
  return (
    <div ref={box} className="chart" style={{ height }}>
      {w > 0 && (
        <svg width={w} height={height} role="img" aria-label={label}>
          <g className="axis">
            {ticks.map((t) => (
              <g key={t}>
                <line className="grid-line" x1={pad.l} x2={pad.l + iw} y1={Y(t)} y2={Y(t)} />
                <text x={pad.l - 8} y={Y(t) + 4} textAnchor="end">
                  {yFmt(t)}
                </text>
              </g>
            ))}
            {data.map(
              (d, i) =>
                (tick ? tick(d) : i % every === 0) && (
                  <text key={i} x={pad.l + band * i + band / 2} y={height - 5} textAnchor="middle">
                    {xFmt ? xFmt(d) : d.label}
                  </text>
                ),
            )}
          </g>
          {joined && (
            <path
              d={stepPath(data.map((d) => Y(d.v)), pad.l, band, pad.t + ih)}
              fill="var(--chart)"
              opacity={hi == null ? 0.85 : 0.4}
              pointerEvents="none"
            />
          )}
          {data.map((d, i) => {
            const x = pad.l + band * i + (band - bw) / 2
            const y = Y(d.v)
            const h = pad.t + ih - y
            const r = Math.min(3, bw / 2, h)
            const p =
              h <= 0 || (joined && hi !== i)
                ? ''
                : joined
                  ? `M${x} ${pad.t + ih} L${x} ${y} L${x + bw} ${y} L${x + bw} ${pad.t + ih} Z`
                  : `M${x} ${pad.t + ih} L${x} ${y + r} Q${x} ${y} ${x + r} ${y} L${x + bw - r} ${y} Q${x + bw} ${y} ${x + bw} ${y + r} L${x + bw} ${pad.t + ih} Z`
            return (
              <g
                key={i}
                onPointerEnter={() => setHi(i)}
                onPointerLeave={() => setHi(null)}
                tabIndex={0}
                onFocus={() => setHi(i)}
                onBlur={() => setHi(null)}
                aria-label={(tip ? tip(d) : d.label) + ': ' + yFmt(d.v)}
              >
                <rect x={pad.l + band * i} y={pad.t} width={band} height={ih} fill="transparent" />
                {p && <path d={p} fill="var(--chart)" opacity={joined ? 0.75 : hi == null || hi === i ? 0.85 : 0.4} />}
              </g>
            )
          })}
        </svg>
      )}
      {hi != null && data[hi] && (
        <div className="chart-tip" style={{ left: clamp(pad.l + band * hi + band / 2, 60, w - 60), top: Y(data[hi].v) }}>
          <span className="v">{yFmt(data[hi].v)}</span> <span className="l">{tip ? tip(data[hi]) : data[hi].label}</span>
        </div>
      )}
    </div>
  )
}

export interface HeatCell {
  dow: number
  hour: number
  avg: number
}

export function Heatmap({ cells }: { cells: HeatCell[] }) {
  const max = Math.max(0.01, ...cells.map((c) => c.avg))
  const step = (v: number) => (v <= 0.05 ? 0 : Math.min(5, Math.ceil((v / max) * 5)))
  const fill = (n: number) => (n === 0 ? 'var(--hover)' : `color-mix(in srgb, var(--chart) ${[0, 14, 28, 46, 66, 90][n]}%, var(--hover))`)
  const days = [1, 2, 3, 4, 5, 6, 0]
  const at = new Map(cells.map((c) => [c.dow * 24 + c.hour, c]))
  return (
    <div>
      <div className="heat" role="img" aria-label="Average players by weekday and hour">
        <span />
        {Array.from({ length: 24 }, (_, h) => (
          <span key={h}>{h % 6 === 0 ? pad2(h) : ''}</span>
        ))}
        {days.map((d) => (
          <Fragment key={d}>
            <span>{DOW[d]}</span>
            {Array.from({ length: 24 }, (_, h) => {
              const avg = at.get(d * 24 + h)?.avg ?? 0
              return <span key={h} className="c" style={{ background: fill(step(avg)) }} data-tip={`${DOW_FULL[d]}s around ${pad2(h)}:00, ${avg.toFixed(1)} on average`} />
            })}
          </Fragment>
        ))}
      </div>
      <div className="row tiny t3" style={{ gap: 5, marginTop: 10, justifyContent: 'flex-end' }}>
        Quiet{' '}
        {[0, 1, 2, 3, 4, 5].map((n) => (
          <span key={n} style={{ width: 12, height: 10, borderRadius: 2, background: fill(n) }} />
        ))}{' '}
        Busy
      </div>
    </div>
  )
}

export interface UptimeSegment {
  start: number
  end: number | null
  reason?: string
}

export function UptimeStrip({ segs, from, to }: { segs: UptimeSegment[]; from: number; to: number }) {
  const span = to - from || 1
  return (
    <div>
      <div style={{ position: 'relative', height: 20, borderRadius: 6, background: 'var(--hover)', overflow: 'hidden' }} role="img" aria-label="When the server was up">
        {segs.map((g, i) => {
          const a = Math.max(g.start, from)
          const b = Math.min(g.end || to, to)
          if (b <= a) return null
          return (
            <span
              key={i}
              data-tip={`Up ${fmtDate(a)} to ${g.end ? fmtDate(b) : 'now'}${g.reason === 'crash' ? ', then crashed' : ''}`}
              style={{
                position: 'absolute',
                top: 0,
                bottom: 0,
                left: ((a - from) / span) * 100 + '%',
                width: `max(2px, ${((b - a) / span) * 100}% - 2px)`,
                background: 'var(--text-3)',
                opacity: 0.6,
              }}
            />
          )
        })}
        {segs
          .filter((g) => g.reason === 'crash' && g.end != null && g.end > from)
          .map((g, i) => (
            <span
              key={'c' + i}
              data-tip={'Crashed ' + fmtDate(g.end as number)}
              style={{ position: 'absolute', top: 0, bottom: 0, left: `calc(${(((g.end as number) - from) / span) * 100}% - 1px)`, width: 3, background: 'var(--bad)' }}
            />
          ))}
      </div>
      <div className="row tiny t3" style={{ justifyContent: 'space-between', marginTop: 6 }}>
        <span>{fmtDate(from, false)}</span>
        <span>Now</span>
      </div>
    </div>
  )
}

export function Spark({ values, w = 120, h = 24, max, min = 0, fluid }: { values: number[]; w?: number; h?: number; max?: number; min?: number; fluid?: boolean }) {
  if (!values || values.length < 2) return <svg width={w} height={h} aria-hidden="true" />
  const mx = max != null ? max : Math.max(...values) || 1
  const pts = values.map((v, i) => [(i / (values.length - 1)) * w, h - 2 - ((v - min) / (mx - min || 1)) * (h - 4)])
  const d = pts.map((p, i) => (i ? 'L' : 'M') + p[0].toFixed(1) + ' ' + p[1].toFixed(1)).join(' ')
  if (fluid) {
    return (
      <svg width="100%" height={h} viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" aria-hidden="true" style={{ display: 'block', overflow: 'visible' }}>
        <path d={d} fill="none" stroke="var(--chart)" strokeWidth="1.3" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
      </svg>
    )
  }
  return (
    <svg width={w} height={h} aria-hidden="true" style={{ display: 'block', overflow: 'visible' }}>
      <path d={d} fill="none" stroke="var(--chart)" strokeWidth="1.3" strokeLinejoin="round" />
    </svg>
  )
}
