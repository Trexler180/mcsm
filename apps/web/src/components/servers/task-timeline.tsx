import { useEffect, useMemo, useState } from "react";
import type { ScheduledTask } from "@/lib/types";
import { describeAction } from "@/lib/cron";

const DAY = 24 * 60 * 60 * 1000;
const HOUR = 60 * 60 * 1000;

// relLabel renders a signed, coarse offset from now for the time axis:
// "now", "-2h", "+3d". Absolute times live in marker tooltips.
function relLabel(deltaMs: number): string {
  const a = Math.abs(deltaMs);
  if (a < 45_000) return "now";
  const sign = deltaMs < 0 ? "-" : "+";
  const m = Math.round(a / 60_000);
  if (m < 60) return `${sign}${m}m`;
  const h = Math.round(m / 60);
  if (h < 48) return `${sign}${h}h`;
  return `${sign}${Math.round(h / 24)}d`;
}

// magnitude of a positive duration, e.g. "3h 20m", "2d".
function dur(absMs: number): string {
  const m = Math.max(0, Math.round(absMs / 60_000));
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  const d = Math.floor(h / 24);
  return `${d}d ${h % 24}h`;
}

function ms(iso: string | null): number | null {
  if (!iso) return null;
  const t = Date.parse(iso);
  return Number.isNaN(t) ? null : t;
}

interface Ev {
  task: ScheduledTask;
  time: number;
  future: boolean;
}

interface Cluster {
  x: number; // percent
  future: boolean;
  items: Ev[];
}

// clusterEvents groups markers whose positions fall within `threshold` percent
// of each other into one badge, so many tasks sharing a cron time (e.g. every
// task at midnight) collapse to a single "N" instead of a pile of dots.
function clusterEvents(evs: Ev[], xOf: (t: number) => number, threshold: number): Cluster[] {
  const sorted = [...evs].sort((a, b) => a.time - b.time);
  const out: Cluster[] = [];
  for (const e of sorted) {
    const x = xOf(e.time);
    const last = out[out.length - 1];
    if (last && Math.abs(x - last.x) <= threshold) {
      last.items.push(e);
      // Track the cluster at the mean of its members for stable placement.
      last.x = last.items.reduce((s, i) => s + xOf(i.time), 0) / last.items.length;
    } else {
      out.push({ x, future: e.future, items: [e] });
    }
  }
  return out;
}

function clusterTitle(c: Cluster): string {
  return c.items
    .slice(0, 12)
    .map(
      (i) =>
        `${i.task.name} — ${i.future ? "next" : "last"} ${new Date(i.time).toLocaleString()}`,
    )
    .join("\n");
}

interface TaskTimelineProps {
  tasks: ScheduledTask[];
  compact?: boolean;
}

// TaskTimeline lays scheduled runs on a single left→right axis: each task's
// last run is a hollow dot left of the "now" line and its next run an accent
// dot to the right. Markers at (nearly) the same time collapse into a count
// badge, so the view stays legible from a few tasks to dozens. The window
// auto-fits the data, capped at ±7 days. It ticks its own clock.
export function TaskTimeline({ tasks, compact }: TaskTimelineProps) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);

  const events = useMemo<Ev[]>(() => {
    const evs: Ev[] = [];
    for (const task of tasks) {
      const last = ms(task.last_run);
      if (last !== null) evs.push({ task, time: last, future: false });
      const next = task.enabled ? ms(task.next_run) : null;
      if (next !== null) evs.push({ task, time: next, future: true });
    }
    return evs;
  }, [tasks]);

  const { t0, t1 } = useMemo(() => {
    let pastMax = HOUR;
    let futureMax = HOUR;
    for (const e of events) {
      if (e.future) futureMax = Math.max(futureMax, e.time - now);
      else pastMax = Math.max(pastMax, now - e.time);
    }
    pastMax = Math.min(pastMax, 7 * DAY);
    futureMax = Math.min(futureMax, 7 * DAY);
    return { t0: now - pastMax * 1.08, t1: now + futureMax * 1.08 };
  }, [events, now]);

  const span = Math.max(t1 - t0, 1);
  const rawPct = (t: number) => ((t - t0) / span) * 100;
  const pct = (t: number) => Math.max(1.5, Math.min(98.5, rawPct(t)));
  const xNow = rawPct(now);

  const future = useMemo(
    () => clusterEvents(events.filter((e) => e.future), pct, compact ? 4 : 3),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [events, t0, t1, compact],
  );
  const past = useMemo(
    () => clusterEvents(events.filter((e) => !e.future), pct, compact ? 4 : 3),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [events, t0, t1, compact],
  );

  // Caption context: soonest upcoming and most recent past run.
  const soonest = useMemo(
    () =>
      events
        .filter((e) => e.future)
        .sort((a, b) => a.time - b.time)[0] ?? null,
    [events],
  );
  const latest = useMemo(
    () =>
      events
        .filter((e) => !e.future)
        .sort((a, b) => b.time - a.time)[0] ?? null,
    [events],
  );

  const ticks = [0, 25, 50, 75, 100].map((p) => ({
    p,
    label: relLabel(t0 + (span * p) / 100 - now),
  }));

  if (events.length === 0) {
    return (
      <p className="py-6 text-center text-sm text-text-secondary">
        Nothing scheduled to show yet. Enabled tasks appear here with their next
        run; past runs show once a task has fired.
      </p>
    );
  }

  const renderCluster = (c: Cluster, key: number) => {
    const multi = c.items.length > 1;
    const base = "absolute top-1/2 -translate-x-1/2 -translate-y-1/2";
    if (multi) {
      return (
        <span
          key={key}
          className={`${base} z-20 grid h-4 min-w-4 place-items-center rounded-full px-1 text-[10px] font-medium ${
            c.future
              ? "bg-accent text-black ring-2 ring-accent/25"
              : "border border-border bg-surface-2 text-text-secondary"
          }`}
          style={{ left: `${c.x}%` }}
          title={clusterTitle(c)}
        >
          {c.items.length}
        </span>
      );
    }
    const only = c.items[0];
    return (
      <span
        key={key}
        className={`${base} h-2.5 w-2.5 rounded-full ${
          c.future
            ? "z-20 bg-accent ring-2 ring-accent/25"
            : "border border-border bg-surface-2"
        }`}
        style={{ left: `${c.x}%` }}
        title={`${only.task.name} — ${only.future ? "next" : "last"} ${describeAction(only.task.action, only.task.payload)} ${new Date(only.time).toLocaleString()}`}
      />
    );
  };

  return (
    <div className="pt-4 text-xs">
      <div className="relative">
        {/* Track */}
        <div className={`relative ${compact ? "h-7" : "h-9"}`}>
          <div className="absolute top-1/2 h-px w-full -translate-y-1/2 bg-border/40" />
          {/* now marker */}
          <div
            className="absolute inset-y-0 z-10 w-px bg-accent"
            style={{ left: `${xNow}%` }}
          >
            <span className="absolute -top-4 -translate-x-1/2 whitespace-nowrap text-[10px] font-medium text-accent">
              now
            </span>
          </div>
          {past.map(renderCluster)}
          {future.map(renderCluster)}
        </div>

        {/* Relative axis */}
        <div className="relative mt-1 h-4 border-t border-border/50">
          {ticks.map((tk) => (
            <span
              key={tk.p}
              className={`absolute top-0.5 -translate-x-1/2 whitespace-nowrap ${
                tk.label === "now" ? "text-accent" : "text-text-secondary"
              }`}
              style={{ left: `${tk.p}%` }}
            >
              {tk.label}
            </span>
          ))}
        </div>
      </div>

      {/* Caption: the two facts people actually want, in words. */}
      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-text-secondary">
        {soonest && (
          <span>
            Next{" "}
            <span className="text-text-primary">{soonest.task.name}</span> in{" "}
            <span className="font-mono text-text-primary">
              {dur(soonest.time - now)}
            </span>
          </span>
        )}
        {latest && (
          <span>
            Last{" "}
            <span className="text-text-primary">{latest.task.name}</span>{" "}
            {relLabel(latest.time - now)}
          </span>
        )}
      </div>
    </div>
  );
}
