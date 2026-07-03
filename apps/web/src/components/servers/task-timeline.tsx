import { useEffect, useMemo, useState } from "react";
import type { ScheduledTask } from "@/lib/types";
import { describeAction } from "@/lib/cron";

const DAY = 24 * 60 * 60 * 1000;
const HOUR = 60 * 60 * 1000;
const ROW_H = 16; // px between stacked dots in a small cluster

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
  x: number; // percent
  future: boolean;
  items: Ev[];
}

// clusterEvents groups markers within `threshold` percent of each other.
function clusterEvents(evs: Ev[], xOf: (t: number) => number, threshold: number): Cluster[] {
  const sorted = [...evs].sort((a, b) => a.time - b.time);
  const out: Cluster[] = [];
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

function Detail({ ev, now }: { ev: Ev; now: number }) {
  return (
    <li className="text-xs">
      <p className="truncate font-medium text-text-primary" title={ev.task.name}>
        {ev.task.name}
      </p>
      <p className="text-text-secondary">
        {ev.future ? "Next" : "Ran"}{" "}
        <span className="text-text-primary">{new Date(ev.time).toLocaleString()}</span>{" "}
        ({ev.future ? `in ${dur(ev.time - now)}` : relLabel(ev.time - now)})
      </p>
      <p className="truncate text-text-secondary">
        {describeAction(ev.task.action, ev.task.payload)}
      </p>
    </li>
  );
}

// Popover shown when a marker is tapped or hovered. Anchored to `x`, clamped so
// it never runs off either edge.
function Popover({ x, items, now }: { x: number; items: Ev[]; now: number }) {
  const anchor: React.CSSProperties =
    x < 24
      ? { left: 0 }
      : x > 76
        ? { right: 0 }
        : { left: `${x}%`, transform: "translateX(-50%)" };
  return (
    <div
      className="absolute bottom-full z-40 mb-2 w-60 max-w-[80vw] rounded-lg border border-border bg-surface p-3 shadow-xl"
      style={anchor}
      onClick={(e) => e.stopPropagation()}
    >
      {items.length > 1 && (
        <p className="mb-1.5 text-xs font-medium text-text-primary">
          {items.length} {items[0].future ? "upcoming runs" : "past runs"}
        </p>
      )}
      <ul className="space-y-1.5">
        {items.slice(0, 8).map((it, i) => (
          <Detail key={i} ev={it} now={now} />
        ))}
        {items.length > 8 && (
          <li className="text-xs text-text-secondary">+{items.length - 8} more</li>
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
// of "now", next runs to the right. When only a couple of tasks share a slot
// they render as stacked, labelled dots; bigger pile-ups collapse into a count
// badge. Every marker taps/hovers open a detail popover. Ticks its own clock.
export function TaskTimeline({ tasks, compact }: TaskTimelineProps) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);

  const [activeId, setActiveId] = useState<string | null>(null); // tapped/pinned
  const [hoverId, setHoverId] = useState<string | null>(null); // desktop hover

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

  const clusters = useMemo(() => {
    const thr = compact ? 5 : 3;
    const p = clusterEvents(events.filter((e) => !e.future), pct, thr);
    const f = clusterEvents(events.filter((e) => e.future), pct, thr);
    return [...p, ...f];
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [events, t0, t1, compact]);

  // Labels stay legible only on a sparse timeline; a busy one keeps to dots and
  // badges (tap for detail). Small clusters (≤3) get stacked, labelled dots.
  const sparse = clusters.length <= 8;
  let maxStack = 1;
  const labelFlags = clusters.map((c) => {
    const labelled = sparse && c.items.length <= 3;
    if (labelled) maxStack = Math.max(maxStack, c.items.length);
    return labelled;
  });

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

  const trackH = Math.max(compact ? 36 : 44, maxStack * ROW_H + 12);

  const dotClass = (future: boolean, active: boolean) =>
    `h-3 w-3 rounded-full transition-transform ${active ? "scale-125" : ""} ${
      future ? "bg-accent ring-2 ring-accent/25" : "border border-border bg-surface-2"
    }`;

  return (
    <div className="pt-4 text-xs">
      {activeId !== null && (
        <div
          className="fixed inset-0 z-30"
          onClick={(e) => {
            e.stopPropagation();
            setActiveId(null);
          }}
        />
      )}

      <div className="relative">
        <div className="relative" style={{ height: trackH }}>
          <div className="absolute top-1/2 h-px w-full -translate-y-1/2 bg-border/40" />
          <div
            className="pointer-events-none absolute inset-y-0 z-10 w-px bg-accent"
            style={{ left: `${xNow}%` }}
          >
            <span className="absolute -top-4 -translate-x-1/2 whitespace-nowrap text-[10px] font-medium text-accent">
              now
            </span>
          </div>

          {clusters.map((c, ci) => {
            const labelled = labelFlags[ci];

            // Stacked, labelled dots for small clusters on a sparse timeline.
            if (labelled) {
              const labelLeft = c.x > 60;
              return c.items.map((it, j) => {
                const id = `${ci}-${j}`;
                const active = (activeId ?? hoverId) === id;
                const y = (j - (c.items.length - 1) / 2) * ROW_H;
                return (
                  <div
                    key={id}
                    className="absolute z-20"
                    style={{
                      left: `${c.x}%`,
                      top: "50%",
                      transform: `translate(${labelLeft ? "-100%" : "0"}, calc(-50% + ${y}px))`,
                    }}
                  >
                    {active && <Popover x={c.x} items={[it]} now={now} />}
                    <button
                      type="button"
                      className={`flex items-center gap-1 ${labelLeft ? "flex-row-reverse" : ""}`}
                      onClick={(e) => {
                        e.stopPropagation();
                        setActiveId((cur) => (cur === id ? null : id));
                      }}
                      onMouseEnter={() => setHoverId(id)}
                      onMouseLeave={() => setHoverId((cur) => (cur === id ? null : cur))}
                    >
                      <span className={dotClass(it.future, active)} />
                      <span
                        className="max-w-[84px] truncate text-[10px] leading-none text-text-primary"
                        title={it.task.name}
                      >
                        {it.task.name}
                      </span>
                    </button>
                  </div>
                );
              });
            }

            // Otherwise a single dot (size 1) or a count badge (bigger pile-up).
            const id = `${ci}`;
            const active = (activeId ?? hoverId) === id;
            const multi = c.items.length > 1;
            return (
              <div
                key={id}
                className="absolute top-1/2 z-20 -translate-x-1/2 -translate-y-1/2"
                style={{ left: `${c.x}%` }}
              >
                {active && <Popover x={c.x} items={c.items} now={now} />}
                <button
                  type="button"
                  aria-label={`${c.items.length} scheduled run${c.items.length === 1 ? "" : "s"}`}
                  className="grid h-8 w-8 place-items-center"
                  onClick={(e) => {
                    e.stopPropagation();
                    setActiveId((cur) => (cur === id ? null : id));
                  }}
                  onMouseEnter={() => setHoverId(id)}
                  onMouseLeave={() => setHoverId((cur) => (cur === id ? null : cur))}
                >
                  {multi ? (
                    <span
                      className={`grid h-4 min-w-4 place-items-center rounded-full px-1 text-[10px] font-medium transition-transform ${
                        active ? "scale-110" : ""
                      } ${
                        c.future
                          ? "bg-accent text-black ring-2 ring-accent/25"
                          : "border border-border bg-surface-2 text-text-secondary"
                      }`}
                    >
                      {c.items.length}
                    </span>
                  ) : (
                    <span className={dotClass(c.future, active)} />
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
                tk.p === 0 ? "left-0" : tk.p === 100 ? "right-0" : "-translate-x-1/2"
              } ${tk.label === "now" ? "text-accent" : "text-text-secondary"}`}
              style={tk.p === 0 || tk.p === 100 ? undefined : { left: `${tk.p}%` }}
            >
              {tk.label}
            </span>
          ))}
        </div>
      </div>

      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-text-secondary">
        {soonest && (
          <span>
            Next <span className="text-text-primary">{soonest.task.name}</span> in{" "}
            <span className="font-mono text-text-primary">{dur(soonest.time - now)}</span>
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
