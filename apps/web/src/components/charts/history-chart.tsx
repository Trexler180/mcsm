import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { api } from "@/lib/api";

const WINDOWS = [
  { hours: 1, label: "1h" },
  { hours: 24, label: "24h" },
  { hours: 24 * 7, label: "7d" },
];

function formatTick(ts: number, hours: number): string {
  const d = new Date(ts * 1000);
  if (hours <= 24) {
    return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  }
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

// MetricsHistoryChart graphs the sampled resource history the API records for
// every running server (one sample a minute, kept 30 days) — CPU load and
// players online — so "was the server struggling overnight?" has an answer
// without anyone having had the live metrics stream open at the time.
export function MetricsHistoryChart({
  serverId,
  ramMaxMb,
}: {
  serverId: string;
  ramMaxMb?: number;
}) {
  const [hours, setHours] = useState(24);

  const { data } = useQuery({
    queryKey: ["metrics-history", serverId, hours],
    queryFn: () => api.servers.metricsHistory(serverId, hours),
    refetchInterval: 60_000,
  });

  const points = (data?.points ?? []).map((p) => ({
    ...p,
    // Memory as a percentage of the configured heap (falling back to host RAM)
    // so it shares the CPU axis.
    mem_pct:
      ramMaxMb && ramMaxMb > 0
        ? Math.min(100, (p.ram_used_mb / ramMaxMb) * 100)
        : p.ram_total_mb > 0
          ? (p.ram_used_mb / p.ram_total_mb) * 100
          : 0,
  }));

  return (
    <div className="bg-surface rounded-lg border border-border p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="text-xs font-medium uppercase tracking-wide text-text-secondary">
          History
        </span>
        <div className="flex items-center gap-1">
          {WINDOWS.map((w) => (
            <button
              key={w.hours}
              onClick={() => setHours(w.hours)}
              className={`rounded px-2 py-0.5 text-xs transition-colors ${
                hours === w.hours
                  ? "bg-accent/15 text-accent"
                  : "text-text-secondary hover:text-text-primary"
              }`}
            >
              {w.label}
            </button>
          ))}
        </div>
      </div>

      {points.length < 2 ? (
        <div className="flex h-40 items-center justify-center text-xs text-text-secondary">
          Not enough history yet — samples are recorded once a minute while the
          server runs.
        </div>
      ) : (
        <ResponsiveContainer width="100%" height={160}>
          <AreaChart data={points} margin={{ top: 4, right: 4, bottom: 0, left: -14 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.06)" />
            <XAxis
              dataKey="ts"
              tickFormatter={(ts: number) => formatTick(ts, hours)}
              tick={{ fontSize: 10, fill: "#8a8a8a" }}
              tickLine={false}
              axisLine={false}
              minTickGap={40}
            />
            <YAxis
              yAxisId="pct"
              domain={[0, 100]}
              tick={{ fontSize: 10, fill: "#8a8a8a" }}
              tickLine={false}
              axisLine={false}
              width={44}
              tickFormatter={(v: number) => `${v}%`}
            />
            <YAxis yAxisId="players" orientation="right" hide domain={[0, "auto"]} />
            <Tooltip
              contentStyle={{
                background: "#1a1a1a",
                border: "1px solid rgba(255,255,255,0.1)",
                borderRadius: 6,
                fontSize: 12,
              }}
              labelFormatter={(ts) =>
                new Date((ts as number) * 1000).toLocaleString()
              }
              formatter={(value, name) => {
                if (name === "cpu_percent")
                  return [`${(value as number).toFixed(1)}%`, "CPU"];
                if (name === "mem_pct")
                  return [`${(value as number).toFixed(1)}%`, "Memory"];
                return [value as number, "Players"];
              }}
            />
            <Area
              yAxisId="pct"
              type="monotone"
              dataKey="cpu_percent"
              stroke="#22c55e"
              fill="#22c55e"
              fillOpacity={0.12}
              strokeWidth={1.5}
              isAnimationActive={false}
            />
            <Area
              yAxisId="pct"
              type="monotone"
              dataKey="mem_pct"
              stroke="#3b82f6"
              fill="#3b82f6"
              fillOpacity={0.08}
              strokeWidth={1.5}
              isAnimationActive={false}
            />
            <Area
              yAxisId="players"
              type="stepAfter"
              dataKey="players"
              stroke="#eab308"
              fill="#eab308"
              fillOpacity={0.1}
              strokeWidth={1.5}
              isAnimationActive={false}
            />
          </AreaChart>
        </ResponsiveContainer>
      )}
      <div className="mt-1.5 flex items-center gap-4 text-[10px] text-text-secondary">
        <span className="inline-flex items-center gap-1.5">
          <span className="h-1.5 w-1.5 rounded-full bg-green-500" /> CPU
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="h-1.5 w-1.5 rounded-full bg-blue-500" /> Memory
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="h-1.5 w-1.5 rounded-full bg-yellow-500" /> Players
        </span>
      </div>
    </div>
  );
}
