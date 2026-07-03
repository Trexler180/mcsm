import { useEffect, useMemo, useState } from "react";
import type { ScheduledTask } from "@/lib/types";
import { describeAction } from "@/lib/cron";

const DAY = 24 * 60 * 60 * 1000;
const HOUR = 60 * 60 * 1000;

// relLabel renders a signed, coarse offset from now: "now", "-2h", "+3d".
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

// dur renders a positive span, e.g. "3h 20m", "2d 4h".
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
  id: number;
  x: number; // percent
  future: boolean;
  items: Ev[];
}

// clusterEvents groups markers within `threshold` percent of each other so many
// tasks sharing a cron time collapse into one badge instead of a pile of dots.
function clusterEvents(evs: Ev[], xOf: (t: number) => number, threshold: number): Omit<Cluster, "id">[] {
  const sorted = [...evs].sort((a, b) => a.time - b.time);
  const out: Omit<Cluster, "id">[] = [];
  for (const e of sorted) {
    const x = xOf(e.time);
    const last = out[out.length - 1];
    if (last && Math.abs(x - last.x) <= threshold) {
      last.items.push(e);
      last.x = last.items.reduce((s, i) => s + xOf(i.time), 0) / last.items.length;
    } else {
      out.push({ x, future: e.future, items: [e] });
    }
  }
  return out;
}

// ClusterPopover is the detail card shown when a marker is tapped or hovered.
function ClusterPopover({ cluster, now }: { cluster: Cluster; now: number }) {
  // Anchor to the marker's x; align the card so it never runs off either edge.
  const anchor: React.CSSProperties =
    cluster.x < 24
      ? { left: 0 }
      : cluster.x > 76
        ? { right: 0 }
        : { left: `${cluster.x}%`, transform: "translateX(-50%)" };

  return (
    <div
      className="absolute bottom-full z-30 mb-2 w-60 max-w-[80vw] rounded-lg border border-border bg-surface p-3 shadow-xl"
      style={anchor}
      onClick={(e) => e.stopPropagation()}
    >
      {cluster.items.length > 1 && (
        <p className="mb-1.5 text-xs font-medium text-text-primary">
          {cluster.items.length} {cluster.future ? "upcoming runs" : "past runs"}
        </p>
      )}
      <ul className="space-y-1.5">
        {cluster.items.slice(0, 8).map((it, i) => (
          <li key={i} className="text-xs">
            <p className="truncate font-medium text-text-primary" title={it.task.name}>
              {it.task.name}
            </p>
            <p className="text-text-secondary">
              {it.future ? "Next" : "Ran"}{" "}
              <span className="text-text-primary">
                {new Date(it.time).toLocaleString()}
              </span>{" "}
              <span className="text-text-secondary">
                ({it.future ? `in ${dur(it.time - now)}` : relLabel(it.time - now)}
                )
              </span>
            </p>
            <p className="truncate text-text-secondary">
              {describeAction(it.task.action, it.task.payload)}
            </p>
          </li>
        ))}
        {cluster.items.length > 8 && (
          <li className="text-xs text-text-secondary">
            +{cluster.items.length - 8} more
          </li>
        )}
      </ul>
    </div>
  );
}

interface TaskTimelineProps {
  tasks: ScheduledTask[];
  compact?: boolean;
}

// TaskTimeline lays scheduled runs on a single left→right axis: last runs left
// of "now", next runs to the right, with co-scheduled markers clustered into a
// count badge. Markers are tap/click targets (with a hover assist on desktop)
// that open a detail popover, so it works on touch. It ticks its own clock.
export function TaskTimeline({ tasks, compact }: TaskTimelineProps) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);

  const [activeId, setActiveId] = useState<number | null>(null); // tapped/pinned
  const [hoverId, setHoverId] = useState<number | null>(null); // desktop hover

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

  const clusters = useMemo<Cluster[]>(() => {
    const thr = compact ? 5 : 3;
    const p = clusterEvents(events.filter((e) => !e.future), pct, thr);
    const f = clusterEvents(events.filter((e) => e.future), pct, thr);
    return [...p, ...f].map((c, i) => ({ ...c, id: i }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [events, t0, t1, compact]);

  const shownId = activeId ?? hoverId;

  const soonest = useMemo(
    () => events.filter((e) => e.future).sort((a, b) => a.time - b.time)[0] ?? null,
    [events],
  );
  const latest = useMemo(
    () => events.filter((e) => !e.future).sort((a, b) => b.time - a.time)[0] ?? null,
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

  return (
    <div className="pt-4 text-xs">
      {/* Dismiss layer for a pinned popover (tap outside to close). */}
      {activeId !== null && (
        <div
          className="fixed inset-0 z-20"
          onClick={(e) => {
            e.stopPropagation();
            setActiveId(null);
          }}
        />
      )}

      <div className="relative">
        {/* Track */}
        <div className={`relative ${compact ? "h-8" : "h-10"}`}>
          <div className="absolute top-1/2 h-px w-full -translate-y-1/2 bg-border/40" />
          {/* now marker */}
          <div
            className="pointer-events-none absolute inset-y-0 z-10 w-px bg-accent"
            style={{ left: `${xNow}%` }}
          >
            <span className="absolute -top-4 -translate-x-1/2 whitespace-nowrap text-[10px] font-medium text-accent">
              now
            </span>
          </div>

          {clusters.map((c) => {
            const multi = c.items.length > 1;
            const isOpen = shownId === c.id;
            return (
              <div
                key={c.id}
                className="absolute top-1/2 z-20 -translate-x-1/2 -translate-y-1/2"
                style={{ left: `${c.x}%` }}
              >
                {isOpen && <ClusterPopover cluster={c} now={now} />}
                {/* Oversized transparent hit area for comfortable tapping. */}
                <button
                  type="button"
                  aria-label={`${c.items.length} scheduled run${c.items.length === 1 ? "" : "s"}`}
                  className="grid h-8 w-8 place-items-center"
                  onClick={(e) => {
                    e.stopPropagation();
                    setActiveId((cur) => (cur === c.id ? null : c.id));
                  }}
                  onMouseEnter={() => setHoverId(c.id)}
                  onMouseLeave={() => setHoverId((cur) => (cur === c.id ? null : cur))}
                >
                  {multi ? (
                    <span
                      className={`grid h-4 min-w-4 place-items-center rounded-full px-1 text-[10px] font-medium transition-transform ${
                        isOpen ? "scale-110" : ""
                      } ${
                        c.future
                          ? "bg-accent text-black ring-2 ring-accent/25"
                          : "border border-border bg-surface-2 text-text-secondary"
                      }`}
                    >
                      {c.items.length}
                    </span>
                  ) : (
                    <span
                      className={`h-3 w-3 rounded-full transition-transform ${
                        isOpen ? "scale-125" : ""
                      } ${
                        c.future
                          ? "bg-accent ring-2 ring-accent/25"
                          : "border border-border bg-surface-2"
                      }`}
                    />
                  )}
                </button>
              </div>
            );
          })}
        </div>

        {/* Relative axis (ends flush so labels don't clip on narrow screens). */}
        <div className="relative mt-1 h-4 border-t border-border/50">
          {ticks.map((tk) => (
            <span
              key={tk.p}
              className={`absolute top-0.5 whitespace-nowrap ${
                tk.p === 0
                  ? "left-0"
                  : tk.p === 100
                    ? "right-0"
                    : "-translate-x-1/2"
              } ${tk.label === "now" ? "text-accent" : "text-text-secondary"}`}
              style={tk.p === 0 || tk.p === 100 ? undefined : { left: `${tk.p}%` }}
            >
              {tk.label}
            </span>
          ))}
        </div>
      </div>

      {/* Caption: the two facts people want, in words. */}
      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-text-secondary">
        {soonest && (
          <span>
            Next <span className="text-text-primary">{soonest.task.name}</span> in{" "}
            <span className="font-mono text-text-primary">
              {dur(soonest.time - now)}
            </span>
          </span>
        )}
        {latest && (
          <span>
            Last <span className="text-text-primary">{latest.task.name}</span>{" "}
            {relLabel(latest.time - now)}
          </span>
        )}
      </div>
    </div>
  );
}
