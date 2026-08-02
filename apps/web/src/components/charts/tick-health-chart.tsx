import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { C_TPS } from "./colors";

export { C_TPS } from "./colors";

// Tick health is its own entity on these pages, so it gets its own hue family
// (violet for the tick rate, cyan for how long a tick takes) — CPU stays green,
// memory blue, players amber. The p95 line is the same cyan, thinner and
// recessed, because it is the same measurement at a different percentile.
const C_MSPT = "#06b6d4";

// A vanilla tick is 50ms; anything above it means the server is spending longer
// on a tick than it has, which is where TPS starts to fall.
const TICK_BUDGET_MS = 50;

// The tick-health fields the API adds to each metrics-history point. null means
// the helper mod was not reporting for that bucket — never zero, which would
// draw a cliff to the floor for a server that was simply fine and unmonitored.
export type TickPoint = {
  ts: number;
  tps: number | null;
  mspt_avg: number | null;
  mspt_p95: number | null;
};

// Average a bucket's tick samples, ignoring the nulls; a bucket with nothing to
// average stays null so the line breaks instead of dipping.
export function avgOrNull(values: Array<number | null>): number | null {
  let sum = 0;
  let n = 0;
  for (const v of values) {
    if (v != null) {
      sum += v;
      n++;
    }
  }
  return n > 0 ? sum / n : null;
}

// Percentiles and worst-case values consolidate by peak, not by average — a
// bad bucket must not be smoothed away by the good ones around it.
export function maxOrNull(values: Array<number | null>): number | null {
  let max: number | null = null;
  for (const v of values) {
    if (v != null && (max === null || v > max)) max = v;
  }
  return max;
}

// Whether the window has any tick data at all. Callers use this to drop the
// whole section: on a vanilla server (or history predating the mod) the page
// must look exactly as it did before tick health existed.
export function hasTickData(points: TickPoint[]): boolean {
  return points.some((p) => p.tps != null || p.mspt_avg != null);
}

export type TickSummary = {
  avgTps: number;
  worstTps: number;
  avgMspt: number;
  worstP95: number;
};

// Window summary for the stat tiles: averages over the reporting buckets only,
// and the worst bucket for each (lowest TPS, highest p95).
export function tickSummary(points: TickPoint[]): TickSummary | null {
  const tps = points.map((p) => p.tps).filter((v): v is number => v != null);
  const avg = points.map((p) => p.mspt_avg).filter((v): v is number => v != null);
  const p95 = points.map((p) => p.mspt_p95).filter((v): v is number => v != null);
  if (tps.length === 0 && avg.length === 0) return null;
  return {
    avgTps: tps.length ? tps.reduce((s, v) => s + v, 0) / tps.length : 0,
    worstTps: tps.length ? Math.min(...tps) : 0,
    avgMspt: avg.length ? avg.reduce((s, v) => s + v, 0) / avg.length : 0,
    worstP95: p95.length ? Math.max(...p95) : 0,
  };
}

// Tick rate and tick time are different quantities (ticks per second vs
// milliseconds), so they can only share an axis if both are expressed against
// something common. Each has an exact healthy limit — 20 TPS, and the 50ms a
// tick is allowed to take — so both are plotted as a percentage of that limit,
// the same trick that lets CPU and memory share one chart.
//
// The two are read in opposite directions, which is the point: a healthy server
// shows tick rate pinned at the 100% line with tick time flat along the floor.
// Trouble is the two lines converging.
type TickDisplay = TickPoint & {
  tps_pct: number | null;
  mspt_pct: number | null;
  mspt_p95_pct: number | null;
};

function toDisplay(points: TickPoint[]): TickDisplay[] {
  return points.map((p) => ({
    ...p,
    // Averages can float a hair over 20; clamp so "full speed" is exactly the
    // 100% line rather than occasionally poking above it.
    tps_pct: p.tps == null ? null : (Math.min(p.tps, 20) / 20) * 100,
    mspt_pct: p.mspt_avg == null ? null : (p.mspt_avg / TICK_BUDGET_MS) * 100,
    mspt_p95_pct:
      p.mspt_p95 == null ? null : (p.mspt_p95 / TICK_BUDGET_MS) * 100,
  }));
}

// The axis holds 0–100% unless the server actually blew its tick budget, in
// which case it grows to fit. Anchoring at 100 keeps a healthy server reading
// as "flat along the floor" instead of being zoomed until 3ms of ordinary
// jitter looks like a crisis.
function axisMax(rows: TickDisplay[]): number {
  let max = 100;
  for (const r of rows) {
    if (r.mspt_pct != null && r.mspt_pct > max) max = r.mspt_pct;
    if (r.mspt_p95_pct != null && r.mspt_p95_pct > max) max = r.mspt_p95_pct;
  }
  return Math.ceil(max / 25) * 25;
}

const tooltipStyle = {
  background: "#1a1a1a",
  border: "1px solid rgba(255,255,255,0.1)",
  borderRadius: 6,
  fontSize: 12,
} as const;

const axisTick = { fontSize: 10, fill: "#8a8a8a" } as const;
const gridStroke = "rgba(255,255,255,0.06)";

function LegendChip({
  color,
  label,
  faint,
}: {
  color: string;
  label: string;
  faint?: boolean;
}) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span
        className="h-1.5 w-1.5 rounded-full"
        style={{ background: color, opacity: faint ? 0.45 : 1 }}
      />
      {label}
    </span>
  );
}

// Tick health as one chart: tick rate and tick time on a single axis, each
// drawn as a percentage of its own healthy limit (see toDisplay). The dashed
// 100% line means both "20 TPS" and "the whole 50ms budget" at once, so one
// reference mark serves both series.
//
// Values are converted back to real units in the tooltip — nobody thinks in
// "percent of a tick budget", they think in TPS and milliseconds. The axis is
// the shared scale; the tooltip is the truth.
//
// Renders nothing when the window holds no tick data.
export function TickHealthCharts({
  points,
  formatTick,
  formatLabel,
  height = 160,
}: {
  points: TickPoint[];
  /** X-axis tick formatter — supplied by the caller so this chart matches the
      CPU/memory chart it sits with. */
  formatTick: (ts: number) => string;
  /** Tooltip header formatter, same reason. */
  formatLabel: (ts: number) => string;
  height?: number;
}) {
  if (points.length < 2 || !hasTickData(points)) return null;

  const rows = toDisplay(points);
  const top = axisMax(rows);

  return (
    <div>
      <ResponsiveContainer width="100%" height={height}>
        <LineChart data={rows} margin={{ top: 4, right: 4, bottom: 0, left: -14 }}>
          <CartesianGrid strokeDasharray="3 3" stroke={gridStroke} />
          <XAxis
            dataKey="ts"
            tickFormatter={formatTick}
            tick={axisTick}
            tickLine={false}
            axisLine={false}
            minTickGap={40}
          />
          <YAxis
            domain={[0, top]}
            tick={axisTick}
            tickLine={false}
            axisLine={false}
            width={44}
            tickFormatter={(v: number) => `${v}%`}
          />
          <Tooltip
            contentStyle={tooltipStyle}
            labelFormatter={(ts) => formatLabel(ts as number)}
            // Each series maps back to its real unit exactly, so the percentage
            // never has to be shown to anyone.
            formatter={(value, name) => {
              const pct = value as number;
              if (name === "tps_pct") {
                return [`${((pct / 100) * 20).toFixed(2)} TPS`, "Tick rate"];
              }
              const ms = ((pct / 100) * TICK_BUDGET_MS).toFixed(1);
              return [
                `${ms} ms`,
                name === "mspt_p95_pct" ? "p95 tick" : "Tick time",
              ];
            }}
          />
          <ReferenceLine
            y={100}
            stroke="rgba(255,255,255,0.28)"
            strokeDasharray="4 4"
            label={{
              value: "20 TPS · 50ms budget",
              position: "insideTopRight",
              fill: "#8a8a8a",
              fontSize: 10,
            }}
          />
          <Line
            type="monotone"
            dataKey="mspt_p95_pct"
            stroke={C_MSPT}
            strokeOpacity={0.45}
            strokeWidth={1.5}
            dot={false}
            connectNulls={false}
            isAnimationActive={false}
          />
          <Line
            type="monotone"
            dataKey="mspt_pct"
            stroke={C_MSPT}
            strokeWidth={2}
            dot={false}
            connectNulls={false}
            isAnimationActive={false}
          />
          <Line
            type="monotone"
            dataKey="tps_pct"
            stroke={C_TPS}
            strokeWidth={2}
            dot={false}
            connectNulls={false}
            isAnimationActive={false}
          />
        </LineChart>
      </ResponsiveContainer>
      <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-[10px] text-text-secondary">
        <LegendChip color={C_TPS} label="Tick rate (100% = 20 TPS)" />
        <LegendChip color={C_MSPT} label="Tick time (100% = 50ms budget)" />
        <LegendChip color={C_MSPT} label="p95" faint />
      </div>
    </div>
  );
}
