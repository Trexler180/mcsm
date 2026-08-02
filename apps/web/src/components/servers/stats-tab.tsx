import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { api } from "@/lib/api";
import type {
  ActivityCell,
  DailyStat,
  TopPlayer,
  UptimeReport,
} from "@/lib/types";
import { Panel, StatTile } from "./shared";
import { GaplessBar } from "@/components/charts/gapless-bar";
import {
  avgOrNull,
  hasTickData,
  maxOrNull,
  TickHealthCharts,
  tickSummary,
} from "@/components/charts/tick-health-chart";

// Chart palette (validated for the dark surface): players wear amber, CPU
// green, memory blue — the same hue families as the rest of the panel, one
// step deeper for contrast discipline. Color follows the entity everywhere:
// anything "players" on this page is amber.
const C_PLAYERS = "#d97706";
const C_CPU = "#16a34a";
const C_MEM = "#3b82f6";

const WINDOWS = [
  { days: 1, label: "24h" },
  { days: 7, label: "7d" },
  { days: 30, label: "30d" },
  { days: 90, label: "90d" },
  { days: 365, label: "1y" },
  { days: 0, label: "All" },
];

// ── formatting helpers ────────────────────────────────────────────────────────

function fmtDuration(s: number): string {
  if (s < 60) return `${Math.max(0, Math.floor(s))}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 48) {
    const rm = m % 60;
    return rm > 0 ? `${h}h ${rm}m` : `${h}h`;
  }
  const d = Math.floor(h / 24);
  const rh = h % 24;
  return rh > 0 ? `${d}d ${rh}h` : `${d}d`;
}

// Minecraft caps at 20 TPS but rolling averages float a hair above; showing
// "20.1" would just make the number look broken.
function fmtTps(n: number): string {
  return Math.min(n, 20).toFixed(2);
}

function fmtTick(ts: number, windowHours: number): string {
  const d = new Date(ts * 1000);
  if (windowHours <= 24) {
    return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  }
  if (windowHours <= 24 * 7) {
    return d.toLocaleDateString(undefined, { weekday: "short", day: "numeric" });
  }
  if (windowHours <= 24 * 120) {
    return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  }
  return d.toLocaleDateString(undefined, { month: "short", year: "2-digit" });
}

function fmtWhen(ts: number): string {
  return new Date(ts * 1000).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

const tooltipStyle = {
  background: "#1a1a1a",
  border: "1px solid rgba(255,255,255,0.1)",
  borderRadius: 6,
  fontSize: 12,
} as const;

const axisTick = { fontSize: 10, fill: "#8a8a8a" } as const;

// ── history charts ────────────────────────────────────────────────────────────

type HistoryPoint = {
  ts: number;
  cpu_percent: number;
  mem_pct: number;
  players: number;
  tps: number | null;
  mspt_avg: number | null;
  mspt_p95: number | null;
};

// The API returns ≤360 bucketed points; consolidate further so player bars
// stay readable at page width. Resources average (smooth trend), players take
// the bucket peak (a spike must not vanish into an average).
const TARGET_BARS = 72;

function consolidate(points: HistoryPoint[]): HistoryPoint[] {
  if (points.length <= TARGET_BARS) return points;
  const size = Math.ceil(points.length / TARGET_BARS);
  const out: HistoryPoint[] = [];
  for (let i = 0; i < points.length; i += size) {
    const bucket = points.slice(i, i + size);
    const n = bucket.length;
    out.push({
      ts: bucket[Math.floor(n / 2)].ts,
      cpu_percent: bucket.reduce((s, p) => s + p.cpu_percent, 0) / n,
      mem_pct: bucket.reduce((s, p) => s + p.mem_pct, 0) / n,
      players: bucket.reduce((m, p) => Math.max(m, p.players), 0),
      // Tick rate and tick time average over the reporting samples only (a
      // bucket with none stays null and breaks the line); p95 takes the peak
      // so a bad minute is not smoothed away by the good ones beside it.
      tps: avgOrNull(bucket.map((p) => p.tps)),
      mspt_avg: avgOrNull(bucket.map((p) => p.mspt_avg)),
      mspt_p95: maxOrNull(bucket.map((p) => p.mspt_p95)),
    });
  }
  return out;
}

function EmptyChart({ note }: { note: string }) {
  return (
    <div className="flex h-40 items-center justify-center px-6 text-center text-xs text-text-secondary">
      {note}
    </div>
  );
}

function PlayersHistoryChart({
  points,
  windowHours,
}: {
  points: HistoryPoint[];
  windowHours: number;
}) {
  if (points.length < 2) {
    return (
      <EmptyChart note="Not enough history yet — player counts are recorded once a minute while the server runs, and kept forever." />
    );
  }
  return (
    <ResponsiveContainer width="100%" height={180}>
      <BarChart
        data={points}
        margin={{ top: 4, right: 4, bottom: 0, left: -14 }}
        barCategoryGap={0}
        barGap={0}
      >
        <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.06)" />
        <XAxis
          dataKey="ts"
          tickFormatter={(ts: number) => fmtTick(ts, windowHours)}
          tick={axisTick}
          tickLine={false}
          axisLine={false}
          minTickGap={40}
        />
        <YAxis
          domain={[0, "auto"]}
          allowDecimals={false}
          tick={axisTick}
          tickLine={false}
          axisLine={false}
          width={44}
        />
        <Tooltip
          contentStyle={tooltipStyle}
          labelFormatter={(ts) => fmtWhen(ts as number)}
          formatter={(value) => [value as number, "Peak players"]}
        />
        <Bar
          dataKey="players"
          fill={C_PLAYERS}
          isAnimationActive={false}
          shape={<GaplessBar values={points.map((p) => p.players)} />}
        />
      </BarChart>
    </ResponsiveContainer>
  );
}

function ResourcesHistoryChart({
  points,
  windowHours,
}: {
  points: HistoryPoint[];
  windowHours: number;
}) {
  if (points.length < 2) {
    return (
      <EmptyChart note="No resource history in this window — samples exist only while the server runs." />
    );
  }
  return (
    <ResponsiveContainer width="100%" height={180}>
      <AreaChart data={points} margin={{ top: 4, right: 4, bottom: 0, left: -14 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.06)" />
        <XAxis
          dataKey="ts"
          tickFormatter={(ts: number) => fmtTick(ts, windowHours)}
          tick={axisTick}
          tickLine={false}
          axisLine={false}
          minTickGap={40}
        />
        <YAxis
          domain={[0, 100]}
          tick={axisTick}
          tickLine={false}
          axisLine={false}
          width={44}
          tickFormatter={(v: number) => `${v}%`}
        />
        <Tooltip
          contentStyle={tooltipStyle}
          labelFormatter={(ts) => fmtWhen(ts as number)}
          formatter={(value, name) => [
            `${(value as number).toFixed(1)}%`,
            name === "cpu_percent" ? "CPU" : "Memory",
          ]}
        />
        <Area
          type="monotone"
          dataKey="cpu_percent"
          stroke={C_CPU}
          fill={C_CPU}
          fillOpacity={0.12}
          strokeWidth={2}
          isAnimationActive={false}
        />
        <Area
          type="monotone"
          dataKey="mem_pct"
          stroke={C_MEM}
          fill={C_MEM}
          fillOpacity={0.08}
          strokeWidth={2}
          isAnimationActive={false}
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}

// ── uptime & availability ────────────────────────────────────────────────────

// Uptime wears the panel's status green; crashes red. Offline stretches stay
// neutral so the green reads as "share of the bar that was up" at a glance.
const C_ONLINE = "#16a34a";
const C_CRASH = "#dc2626";

function pctOf(ts: number, since: number, span: number): number {
  return Math.min(100, Math.max(0, ((ts - since) / span) * 100));
}

// Availability with up to 3 decimals, trailing zeros trimmed — "99.987%"
// stays distinguishable from "99.9%", and a clean window reads "100%".
function fmtAvailability(pct: number): string {
  return `${parseFloat(pct.toFixed(3))}%`;
}

function UptimeTimeline({
  report,
  since,
  now,
}: {
  report: UptimeReport;
  since: number;
  now: number;
}) {
  const span = Math.max(1, now - since);
  const untrackedPct =
    report.tracked_since > since ? pctOf(report.tracked_since, since, span) : 0;

  return (
    <div>
      <div
        className="relative h-3 overflow-hidden rounded-sm"
        style={{ background: "rgba(255,255,255,0.08)" }}
        role="img"
        aria-label="Uptime timeline for the selected window"
      >
        {/* Lead-in before tracking began: distinct from real downtime. */}
        {untrackedPct > 0 && (
          <div
            className="absolute inset-y-0 left-0"
            style={{
              width: `${untrackedPct}%`,
              background:
                "repeating-linear-gradient(45deg, rgba(255,255,255,0.03), rgba(255,255,255,0.03) 4px, transparent 4px, transparent 8px)",
            }}
            title="Before uptime tracking began"
          />
        )}
        {report.segments.map((seg, i) => {
          const start = Math.max(seg.started_at, since);
          const end = seg.ended_at || now;
          const left = pctOf(start, since, span);
          const width = Math.max(0.15, pctOf(end, since, span) - left);
          const label = seg.ended_at
            ? `Online ${fmtWhen(seg.started_at)} — ${fmtWhen(seg.ended_at)} (${fmtDuration(seg.ended_at - seg.started_at)})${seg.end_reason === "crash" ? " · ended in a crash" : ""}`
            : `Online since ${fmtWhen(seg.started_at)} (${fmtDuration(now - seg.started_at)})`;
          return (
            <div
              key={i}
              className="absolute inset-y-0"
              style={{
                left: `${left}%`,
                width: `${width}%`,
                background: C_ONLINE,
              }}
              title={label}
            />
          );
        })}
        {/* Crash ticks sit on top so they survive next to adjacent restarts. */}
        {report.segments
          .filter((s) => s.end_reason === "crash" && s.ended_at)
          .map((seg, i) => (
            <div
              key={`c${i}`}
              className="absolute inset-y-0 w-[3px]"
              style={{
                left: `calc(${pctOf(seg.ended_at!, since, span)}% - 1px)`,
                background: C_CRASH,
              }}
              title={`Crashed ${fmtWhen(seg.ended_at!)}`}
            />
          ))}
      </div>
      <div className="mt-1 flex items-center justify-between text-[10px] text-text-secondary">
        <span>{fmtWhen(since)}</span>
        <span className="flex items-center gap-3">
          <LegendChip color={C_ONLINE} label="Online" />
          <LegendChip color="rgba(255,255,255,0.25)" label="Offline" />
          <LegendChip color={C_CRASH} label="Crash" />
        </span>
        <span>now</span>
      </div>
    </div>
  );
}

function UptimePanel({
  report,
  since,
  now,
}: {
  report?: UptimeReport;
  since: number;
  now: number;
}) {
  if (!report || report.tracked_since === 0) {
    return (
      <Panel
        title="Uptime & availability"
        description="Online and offline stretches, from status transitions."
      >
        <EmptyChart note="No uptime recorded yet — tracking starts the first time the server comes online." />
      </Panel>
    );
  }

  const interruptions = report.stops + report.crashes;
  const lastEnd = report.segments
    .filter((s) => s.ended_at)
    .reduce((m, s) => Math.max(m, s.ended_at!), 0);

  return (
    <Panel
      title="Uptime & availability"
      description={`Online and offline stretches, from status transitions. Tracked since ${fmtWhen(report.tracked_since)}.`}
    >
      <div className="space-y-4">
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <StatTile
            label="Availability"
            value={
              report.window_seconds > 0
                ? fmtAvailability(report.availability_pct)
                : "—"
            }
            detail={`${fmtDuration(report.uptime_seconds)} online in this window`}
          />
          <StatTile
            label="Current status"
            value={
              report.online_since
                ? `Up ${fmtDuration(now - report.online_since)}`
                : "Offline"
            }
            detail={
              report.online_since
                ? `since ${fmtWhen(report.online_since)}`
                : lastEnd > 0
                  ? `down ${fmtDuration(now - lastEnd)} — since ${fmtWhen(lastEnd)}`
                  : undefined
            }
          />
          <StatTile
            label="Interruptions"
            value={String(interruptions)}
            detail={
              report.crashes > 0
                ? `${report.crashes} crash${report.crashes === 1 ? "" : "es"} · ${report.stops} clean stop${report.stops === 1 ? "" : "s"}`
                : interruptions > 0
                  ? "all clean stops"
                  : "none in this window"
            }
          />
          <StatTile
            label="Longest uptime"
            value={
              report.longest_uptime_seconds > 0
                ? fmtDuration(report.longest_uptime_seconds)
                : "—"
            }
            detail="single online stretch"
          />
        </div>
        <UptimeTimeline report={report} since={since} now={now} />
      </div>
    </Panel>
  );
}

function LegendChip({ color, label }: { color: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-[10px] text-text-secondary">
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: color }} />
      {label}
    </span>
  );
}

// ── activity heatmap ─────────────────────────────────────────────────────────

// Monday-first display order over strftime's Sunday-based %w values.
const DOW_ORDER = [1, 2, 3, 4, 5, 6, 0];
const DOW_LABELS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

function heatColor(avg: number, max: number): string {
  if (avg <= 0 || max <= 0) return "rgba(255,255,255,0.04)";
  // Sequential ramp: one hue (the players amber), alpha-stepped dark→bright so
  // lightness rises monotonically with the value on the dark surface.
  const t = Math.sqrt(Math.min(1, avg / max)); // sqrt lifts the low end into view
  const alpha = 0.15 + 0.85 * t;
  return `rgba(217, 119, 6, ${alpha.toFixed(3)})`;
}

function ActivityHeatmap({ cells }: { cells: ActivityCell[] }) {
  if (cells.length === 0) {
    return (
      <EmptyChart note="No activity recorded in this window yet. The heatmap fills in as hourly history accumulates." />
    );
  }
  const byKey = new Map<number, ActivityCell>();
  let max = 0;
  for (const c of cells) {
    byKey.set(c.dow * 24 + c.hour, c);
    if (c.avg_players > max) max = c.avg_players;
  }

  return (
    <div>
      <div className="min-w-[420px]">
        {/* Hour scale */}
        <div
          className="mb-1 grid gap-[2px]"
          style={{ gridTemplateColumns: "2rem repeat(24, minmax(0, 1fr))" }}
        >
          <span />
          {Array.from({ length: 24 }, (_, h) => (
            <span
              key={h}
              className="text-center text-[9px] leading-none text-text-secondary"
            >
              {h % 6 === 0 ? h : ""}
            </span>
          ))}
        </div>
        {DOW_ORDER.map((dow) => (
          <div
            key={dow}
            className="mb-[2px] grid gap-[2px]"
            style={{ gridTemplateColumns: "2rem repeat(24, minmax(0, 1fr))" }}
          >
            <span className="pr-1 text-right text-[9px] leading-4 text-text-secondary">
              {DOW_LABELS[dow]}
            </span>
            {Array.from({ length: 24 }, (_, h) => {
              const cell = byKey.get(dow * 24 + h);
              const avg = cell?.avg_players ?? 0;
              const label = `${DOW_LABELS[dow]} ${String(h).padStart(2, "0")}:00 — ${
                cell
                  ? `avg ${avg.toFixed(1)} player${avg === 1 ? "" : "s"}, peak ${cell.max_players}`
                  : "no data"
              }`;
              return (
                <div
                  key={h}
                  role="img"
                  aria-label={label}
                  title={label}
                  className="h-4 rounded-[3px]"
                  style={{ background: heatColor(avg, max) }}
                />
              );
            })}
          </div>
        ))}
      </div>
      <div className="mt-2 flex items-center justify-end gap-1.5 text-[10px] text-text-secondary">
        Less
        {[0, 0.25, 0.5, 0.75, 1].map((t) => (
          <span
            key={t}
            className="h-2.5 w-2.5 rounded-[2px]"
            style={{ background: t === 0 ? "rgba(255,255,255,0.04)" : heatColor(t * t * max, max) }}
          />
        ))}
        More
      </div>
    </div>
  );
}

// ── daily unique players ─────────────────────────────────────────────────────

function DailyChart({ daily }: { daily: DailyStat[] }) {
  if (daily.length < 2) {
    return (
      <EmptyChart note="Needs at least a couple of days of visits — check back tomorrow." />
    );
  }
  const points = daily.map((d) => ({
    ...d,
    ts: new Date(`${d.date}T12:00:00`).getTime() / 1000,
  }));
  return (
    <ResponsiveContainer width="100%" height={180}>
      <BarChart
        data={points}
        margin={{ top: 4, right: 4, bottom: 0, left: -14 }}
        barCategoryGap={0}
        barGap={0}
      >
        <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.06)" />
        <XAxis
          dataKey="ts"
          tickFormatter={(ts: number) => fmtTick(ts, 25)}
          tick={axisTick}
          tickLine={false}
          axisLine={false}
          minTickGap={40}
        />
        <YAxis
          domain={[0, "auto"]}
          allowDecimals={false}
          tick={axisTick}
          tickLine={false}
          axisLine={false}
          width={44}
        />
        <Tooltip
          contentStyle={tooltipStyle}
          labelFormatter={(ts) =>
            new Date((ts as number) * 1000).toLocaleDateString(undefined, {
              weekday: "short",
              month: "short",
              day: "numeric",
              year: "numeric",
            })
          }
          formatter={(value, name) => {
            if (name === "unique_players") return [value as number, "Unique players"];
            if (name === "joins") return [value as number, "Joins"];
            return [value as number, String(name)];
          }}
        />
        <Bar
          dataKey="unique_players"
          fill={C_PLAYERS}
          isAnimationActive={false}
          shape={<GaplessBar values={points.map((p) => p.unique_players)} />}
        />
      </BarChart>
    </ResponsiveContainer>
  );
}

// ── top players table ────────────────────────────────────────────────────────

function TopPlayersTable({ players }: { players: TopPlayer[] }) {
  if (players.length === 0) {
    return (
      <EmptyChart note="Nobody has visited in this window. Sessions are tracked automatically while the server runs." />
    );
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <thead>
          <tr className="border-b border-border text-[10px] uppercase tracking-wide text-text-secondary">
            <th className="w-8 py-2 pr-2 font-medium">#</th>
            <th className="py-2 pr-4 font-medium">Player</th>
            <th className="py-2 pr-4 font-medium">Playtime</th>
            <th className="hidden py-2 pr-4 font-medium sm:table-cell">Joins</th>
            <th className="py-2 font-medium">Last seen</th>
          </tr>
        </thead>
        <tbody>
          {players.map((p, i) => (
            <tr key={p.name.toLowerCase()} className="border-b border-border/50">
              <td className="py-2 pr-2 text-xs text-text-secondary">{i + 1}</td>
              <td className="py-2 pr-4">
                <span className="inline-flex items-center gap-2 text-text-primary">
                  {p.name}
                  {p.online && (
                    <span
                      className="inline-flex items-center gap-1 text-[10px] text-green-400"
                      title="Online now"
                    >
                      <span className="h-1.5 w-1.5 rounded-full bg-green-400" />
                      online
                    </span>
                  )}
                </span>
              </td>
              <td className="py-2 pr-4 text-text-primary">
                {fmtDuration(p.playtime_seconds)}
              </td>
              <td className="hidden py-2 pr-4 text-text-secondary sm:table-cell">
                {p.joins}
              </td>
              <td className="py-2 text-xs text-text-secondary">
                {p.online
                  ? "now"
                  : new Date(p.last_seen * 1000).toLocaleDateString(undefined, {
                      month: "short",
                      day: "numeric",
                    })}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ── the tab ──────────────────────────────────────────────────────────────────

export function StatsTab({
  serverId,
  ramMaxMb,
}: {
  serverId: string;
  ramMaxMb?: number;
}) {
  const [days, setDays] = useState(30);

  const { data: stats } = useQuery({
    queryKey: ["server-stats", serverId, days],
    queryFn: () => api.servers.stats(serverId, days),
    refetchInterval: 60_000,
  });
  const { data: history } = useQuery({
    queryKey: ["metrics-history", serverId, "stats", days],
    queryFn: () => api.servers.metricsHistory(serverId, days * 24),
    refetchInterval: 60_000,
  });

  const windowHours = history?.window_hours || (days > 0 ? days * 24 : 24);
  const points = consolidate(
    (history?.points ?? []).map((p) => ({
      ts: p.ts,
      cpu_percent: p.cpu_percent,
      players: p.players,
      tps: p.tps ?? null,
      mspt_avg: p.mspt_avg ?? null,
      mspt_p95: p.mspt_p95 ?? null,
      mem_pct:
        ramMaxMb && ramMaxMb > 0
          ? Math.min(100, (p.ram_used_mb / ramMaxMb) * 100)
          : p.ram_total_mb > 0
            ? (p.ram_used_mb / p.ram_total_mb) * 100
            : 0,
    })),
  );

  // Tick health only exists for servers running the helper mod; everything
  // below is gated on this being non-null so a vanilla server's Stats tab is
  // byte-for-byte the page it was before.
  const tick = hasTickData(points) ? tickSummary(points) : null;

  const s = stats?.summary;
  const windowSeconds = stats
    ? Math.max(1, Math.floor(Date.now() / 1000) - stats.since)
    : 0;
  const uptimePct = s && windowSeconds > 0
    ? Math.min(100, (s.uptime_seconds / windowSeconds) * 100)
    : 0;

  return (
    <div className="space-y-4">
      {/* Header: title + the one window selector the whole page follows */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="text-sm font-semibold text-text-primary">Statistics</h2>
          <p className="text-xs text-text-secondary">
            {stats && stats.data_since > 0
              ? `History since ${new Date(stats.data_since * 1000).toLocaleDateString()} — old data is rolled up hourly, never deleted.`
              : "History accumulates while the server runs."}
          </p>
        </div>
        <div className="flex items-center gap-1" role="tablist" aria-label="Stats window">
          {WINDOWS.map((w) => (
            <button
              key={w.days}
              role="tab"
              aria-selected={days === w.days}
              onClick={() => setDays(w.days)}
              className={`rounded px-2 py-1 text-xs transition-colors ${
                days === w.days
                  ? "bg-accent/15 text-accent"
                  : "text-text-secondary hover:text-text-primary"
              }`}
            >
              {w.label}
            </button>
          ))}
        </div>
      </div>

      {/* Headline tiles */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
        <StatTile
          label="Peak players"
          value={s ? String(s.peak_players) : "—"}
          detail={s?.peak_ts ? fmtWhen(s.peak_ts) : undefined}
        />
        <StatTile
          label="Unique players"
          value={s ? String(s.unique_players) : "—"}
        />
        <StatTile label="Joins" value={s ? String(s.total_joins) : "—"} />
        <StatTile
          label="Playtime"
          value={s ? fmtDuration(s.playtime_seconds) : "—"}
          detail={
            s && s.total_joins > 0
              ? `~${fmtDuration(s.playtime_seconds / s.total_joins)} per visit`
              : undefined
          }
        />
        <StatTile
          label="Longest session"
          value={s ? fmtDuration(s.longest_session_seconds) : "—"}
          detail={s?.longest_session_player || undefined}
        />
        <StatTile
          label="Server uptime"
          value={s ? fmtDuration(s.uptime_seconds) : "—"}
          detail={s ? `${uptimePct.toFixed(0)}% of window` : undefined}
        />
      </div>

      {/* Uptime & availability */}
      <UptimePanel
        report={stats?.uptime}
        since={stats?.since ?? Math.floor(Date.now() / 1000) - 86400}
        now={Math.floor(Date.now() / 1000)}
      />

      {/* Tick health leads the charts: it is the measure of whether the server
          was actually pleasant to play on. Players and host resources follow as
          the context for it. Hidden entirely when the helper mod reported
          nothing in this window. */}
      {tick && (
        <Panel
          title="Tick health"
          description="Server tick rate and how long each tick took, reported by the helper mod."
        >
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
              <StatTile
                label="Average TPS"
                value={fmtTps(tick.avgTps)}
                detail="20 is full speed"
              />
              <StatTile
                label="Worst TPS"
                value={fmtTps(tick.worstTps)}
                detail="lowest interval in this window"
              />
              <StatTile
                label="Average tick time"
                value={`${tick.avgMspt.toFixed(1)} ms`}
                detail="of the 50 ms budget"
              />
              <StatTile
                label="Worst p95 tick"
                value={`${tick.worstP95.toFixed(1)} ms`}
                detail={
                  tick.worstP95 > 50
                    ? "over budget — players would feel this"
                    : "inside the budget"
                }
              />
            </div>
            <TickHealthCharts
              points={points}
              formatTick={(ts) => fmtTick(ts, windowHours)}
              formatLabel={fmtWhen}
              height={160}
            />
          </div>
        </Panel>
      )}

      {/* Players over time */}
      <Panel
        title="Players online"
        description="Peak concurrent players per interval."
      >
        <PlayersHistoryChart points={points} windowHours={windowHours} />
      </Panel>

      {/* Activity heatmap + daily uniques */}
      <div className="grid gap-4 xl:grid-cols-2">
        <Panel
          title="Activity by hour"
          description="Average players for each hour of the week, in your timezone."
          bodyClassName=" overflow-x-auto"
        >
          <ActivityHeatmap cells={stats?.activity ?? []} />
        </Panel>
        <Panel
          title="Unique players per day"
          description="How many different players joined each day."
        >
          <DailyChart daily={stats?.daily ?? []} />
        </Panel>
      </div>

      {/* Resources */}
      <Panel
        title="CPU & memory"
        description="Sampled once a minute while the server runs."
        actions={
          <div className="flex items-center gap-3">
            <LegendChip color={C_CPU} label="CPU" />
            <LegendChip color={C_MEM} label="Memory" />
          </div>
        }
      >
        <ResourcesHistoryChart points={points} windowHours={windowHours} />
      </Panel>

      {/* Leaderboard */}
      <Panel
        title="Top players"
        description="Ranked by time played in the selected window."
      >
        <TopPlayersTable players={stats?.top_players ?? []} />
      </Panel>
    </div>
  );
}
