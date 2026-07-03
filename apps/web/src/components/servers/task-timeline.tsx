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

function ms(iso: string | null): number | null {
  if (!iso) return null;
  const t = Date.parse(iso);
  return Number.isNaN(t) ? null : t;
}

interface TaskTimelineProps {
  tasks: ScheduledTask[];
  // Cap the number of task lanes; the rest collapse into a "+N more" note.
  maxLanes?: number;
  compact?: boolean;
}

// TaskTimeline lays scheduled tasks along a left→right time axis: each task is
// a lane, its last run a muted dot on the left of the "now" line and its next
// run an accent dot on the right, with a faint connector spanning the gap. The
// window auto-fits the data (capped at ±7 days) so recurring tasks stay
// readable. It ticks its own clock so callers don't have to.
export function TaskTimeline({ tasks, maxLanes = 8, compact }: TaskTimelineProps) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    // The markers drift slowly; a 30s tick keeps the axis honest cheaply.
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);

  const lanes = useMemo(() => {
    // Show tasks that have something to plot (a past run or an upcoming one),
    // upcoming-soonest first, then most-recently-run.
    const withTimes = tasks
      .map((t) => ({ task: t, last: ms(t.last_run), next: t.enabled ? ms(t.next_run) : null }))
      .filter((l) => l.last !== null || l.next !== null);
    withTimes.sort((a, b) => {
      if (a.next !== null && b.next !== null) return a.next - b.next;
      if (a.next !== null) return -1;
      if (b.next !== null) return 1;
      return (b.last ?? 0) - (a.last ?? 0);
    });
    return withTimes;
  }, [tasks]);

  const { t0, t1 } = useMemo(() => {
    let pastMax = HOUR;
    let futureMax = HOUR;
    for (const l of lanes) {
      if (l.last !== null) pastMax = Math.max(pastMax, now - l.last);
      if (l.next !== null) futureMax = Math.max(futureMax, l.next - now);
    }
    // Keep the scale legible when a distant monthly task would otherwise
    // compress everything: cap the visible window at a week each way.
    pastMax = Math.min(pastMax, 7 * DAY);
    futureMax = Math.min(futureMax, 7 * DAY);
    return { t0: now - pastMax * 1.08, t1: now + futureMax * 1.08 };
  }, [lanes, now]);

  const span = Math.max(t1 - t0, 1);
  const pct = (t: number) => ((t - t0) / span) * 100;
  const clampPct = (p: number) => Math.max(1.5, Math.min(98.5, p));
  const xNow = pct(now);

  const ticks = [0, 25, 50, 75, 100].map((p) => ({
    p,
    label: relLabel(t0 + (span * p) / 100 - now),
  }));

  if (lanes.length === 0) {
    return (
      <p className="py-6 text-center text-sm text-text-secondary">
        Nothing scheduled to show yet. Enabled tasks appear here with their next
        run; past runs show once a task has fired.
      </p>
    );
  }

  const shown = lanes.slice(0, maxLanes);
  const overflow = lanes.length - shown.length;
  const laneH = compact ? "h-6" : "h-7";
  const nameW = compact ? 92 : 116;

  return (
    <div className="text-xs">
      <div className="flex">
        {/* Task name gutter */}
        <div className="flex-shrink-0" style={{ width: nameW }}>
          {shown.map((l) => (
            <div key={l.task.id} className={`flex items-center pr-2 ${laneH}`}>
              <span className="truncate text-text-primary" title={l.task.name}>
                {l.task.name}
              </span>
            </div>
          ))}
        </div>

        {/* Time track */}
        <div className="relative flex-1">
          <div className="relative">
            {/* now marker line spanning every lane */}
            <div
              className="absolute inset-y-0 z-10 w-px bg-accent"
              style={{ left: `${xNow}%` }}
            >
              <span className="absolute -top-4 -translate-x-1/2 whitespace-nowrap text-[10px] font-medium text-accent">
                now
              </span>
            </div>

            {shown.map((l) => {
              const xLast = l.last !== null ? clampPct(pct(l.last)) : null;
              const xNext = l.next !== null ? clampPct(pct(l.next)) : null;
              return (
                <div key={l.task.id} className={`relative ${laneH}`}>
                  {/* baseline */}
                  <div className="absolute top-1/2 h-px w-full -translate-y-1/2 bg-border/40" />
                  {/* connector from last→next through now */}
                  {xLast !== null && xNext !== null && (
                    <div
                      className="absolute top-1/2 h-0.5 -translate-y-1/2 rounded bg-accent/25"
                      style={{
                        left: `${Math.min(xLast, xNext)}%`,
                        width: `${Math.abs(xNext - xLast)}%`,
                      }}
                    />
                  )}
                  {xLast !== null && (
                    <span
                      className="absolute top-1/2 h-2.5 w-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full border border-border bg-surface-2"
                      style={{ left: `${xLast}%` }}
                      title={`${l.task.name} — last ran ${new Date(l.last!).toLocaleString()}`}
                    />
                  )}
                  {xNext !== null && (
                    <span
                      className="absolute top-1/2 z-20 h-2.5 w-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-accent ring-2 ring-accent/25"
                      style={{ left: `${xNext}%` }}
                      title={`${l.task.name} — next ${describeAction(l.task.action, l.task.payload)} at ${new Date(l.next!).toLocaleString()}`}
                    />
                  )}
                </div>
              );
            })}
          </div>

          {/* Relative time axis */}
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
      </div>

      <div className="mt-2 flex items-center gap-4 text-[10px] text-text-secondary">
        <span className="inline-flex items-center gap-1.5">
          <span className="h-2 w-2 rounded-full bg-accent ring-2 ring-accent/25" />
          Next run
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="h-2 w-2 rounded-full border border-border bg-surface-2" />
          Last run
        </span>
        {overflow > 0 && <span className="ml-auto">+{overflow} more</span>}
      </div>
    </div>
  );
}
