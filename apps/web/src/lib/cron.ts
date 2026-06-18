// Human-language helpers for scheduled tasks. Keeps cron <-> English and the
// action labels in one place so the tasks UI never has to show raw operator
// syntax (e.g. "0 3 * * * · mod_update") to a human.

export type ScheduleKind = "hourly" | "daily" | "weekly" | "monthly" | "custom";

export interface ScheduleState {
  kind: ScheduleKind;
  minute: number; // 0-59
  hour: number; // 0-23
  weekday: number; // 0-6 (Sun-Sat), used by "weekly"
  day: number; // 1-31, used by "monthly"
  /** Raw cron expression, authoritative when kind === "custom". */
  raw: string;
}

const WEEKDAYS = [
  "Sunday",
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
];

const MONTHS = [
  "January",
  "February",
  "March",
  "April",
  "May",
  "June",
  "July",
  "August",
  "September",
  "October",
  "November",
  "December",
];

/** Default builder state for a brand-new task: every day at 4:00 AM. */
export const DEFAULT_SCHEDULE: ScheduleState = {
  kind: "daily",
  minute: 0,
  hour: 4,
  weekday: 1,
  day: 1,
  raw: "0 4 * * *",
};

function clampInt(v: number, lo: number, hi: number): number {
  if (!Number.isFinite(v)) return lo;
  return Math.min(hi, Math.max(lo, Math.trunc(v)));
}

/** "3:00 AM" / "11:30 PM" from 24h minute/hour. */
export function formatTime(hour: number, minute: number): string {
  const h = clampInt(hour, 0, 23);
  const m = clampInt(minute, 0, 59);
  const period = h < 12 ? "AM" : "PM";
  const h12 = h % 12 === 0 ? 12 : h % 12;
  return `${h12}:${String(m).padStart(2, "0")} ${period}`;
}

function ordinal(n: number): string {
  const s = ["th", "st", "nd", "rd"];
  const v = n % 100;
  return n + (s[(v - 20) % 10] || s[v] || s[0]);
}

// Treat a field as a plain non-negative integer, or null when it's not (e.g.
// "*", "*/5", "1-5", "0,6"). Callers fall back to the raw expression for shapes
// we don't model.
function asInt(field: string): number | null {
  return /^\d+$/.test(field) ? Number(field) : null;
}

/** Build a 5-field cron string from builder state (ignored for "custom"). */
export function buildCron(s: ScheduleState): string {
  const m = clampInt(s.minute, 0, 59);
  const h = clampInt(s.hour, 0, 23);
  switch (s.kind) {
    case "hourly":
      return `${m} * * * *`;
    case "daily":
      return `${m} ${h} * * *`;
    case "weekly":
      return `${m} ${h} * * ${clampInt(s.weekday, 0, 6)}`;
    case "monthly":
      return `${m} ${h} ${clampInt(s.day, 1, 31)} * *`;
    case "custom":
    default:
      return s.raw.trim();
  }
}

/**
 * Parse an existing cron expression back into builder state. Recognizes the
 * shapes the builder can produce; anything else becomes kind "custom" so the
 * raw editor takes over without losing the expression.
 */
export function parseCron(expr: string): ScheduleState {
  const raw = (expr || "").trim();
  const base: ScheduleState = { ...DEFAULT_SCHEDULE, raw, kind: "custom" };
  const parts = raw.split(/\s+/);
  if (parts.length !== 5) return base;

  const [min, hr, dom, mon, dow] = parts;
  const m = asInt(min);
  const h = asInt(hr);

  // Hourly: a fixed minute every hour.
  if (m !== null && hr === "*" && dom === "*" && mon === "*" && dow === "*") {
    return { ...base, kind: "hourly", minute: m };
  }
  if (m === null || h === null) return base;

  // Daily: fixed minute + hour, every day.
  if (dom === "*" && mon === "*" && dow === "*") {
    return { ...base, kind: "daily", minute: m, hour: h };
  }
  // Weekly: a single weekday.
  if (dom === "*" && mon === "*" && asInt(dow) !== null) {
    const wd = asInt(dow)!;
    if (wd <= 6) return { ...base, kind: "weekly", minute: m, hour: h, weekday: wd };
  }
  // Monthly: a single day-of-month.
  if (mon === "*" && dow === "*" && asInt(dom) !== null) {
    return { ...base, kind: "monthly", minute: m, hour: h, day: asInt(dom)! };
  }
  return base;
}

function describeWeekday(dow: string): string | null {
  if (dow === "1-5") return "weekdays";
  if (["0,6", "6,0", "0,7", "7,0"].includes(dow)) return "weekends";
  const wd = asInt(dow);
  if (wd !== null) return WEEKDAYS[wd % 7] ?? null;
  // Comma list of single days, e.g. "1,3,5".
  if (/^\d+(,\d+)+$/.test(dow)) {
    const names = dow.split(",").map((d) => WEEKDAYS[Number(d) % 7]);
    if (names.every(Boolean)) return names.join(", ");
  }
  return null;
}

/**
 * Render a 5-field cron expression as plain English. Falls back to the raw
 * expression (prefixed) for shapes we don't model, so it's always safe to show.
 */
export function describeCron(expr: string): string {
  const raw = (expr || "").trim();
  const parts = raw.split(/\s+/);
  if (parts.length !== 5) return raw || "—";
  const [min, hr, dom, mon, dow] = parts;

  // Every minute / every N minutes.
  if (min === "*" && hr === "*" && dom === "*" && mon === "*" && dow === "*") {
    return "Every minute";
  }
  const stepMatch = /^\*\/(\d+)$/.exec(min);
  if (stepMatch && hr === "*" && dom === "*" && mon === "*" && dow === "*") {
    return `Every ${stepMatch[1]} minutes`;
  }

  const m = asInt(min);
  if (m === null) return `Custom (${raw})`;

  // Hourly.
  if (hr === "*" && dom === "*" && mon === "*" && dow === "*") {
    return m === 0 ? "Every hour, on the hour" : `Every hour at :${String(m).padStart(2, "0")}`;
  }

  const h = asInt(hr);
  if (h === null) return `Custom (${raw})`;
  const time = formatTime(h, m);

  // Weekly.
  if (dom === "*" && mon === "*" && dow !== "*") {
    const wd = describeWeekday(dow);
    return wd ? `Every ${wd} at ${time}` : `Custom (${raw})`;
  }
  // Daily.
  if (dom === "*" && mon === "*" && dow === "*") {
    return `Every day at ${time}`;
  }
  // Monthly.
  if (mon === "*" && dow === "*" && asInt(dom) !== null) {
    return `Monthly on the ${ordinal(asInt(dom)!)} at ${time}`;
  }
  // Yearly.
  if (dow === "*" && asInt(dom) !== null && asInt(mon) !== null) {
    const month = MONTHS[(asInt(mon)! - 1 + 12) % 12];
    if (month) return `Yearly on ${month} ${ordinal(asInt(dom)!)} at ${time}`;
  }
  return `Custom (${raw})`;
}

export const WEEKDAY_OPTIONS = WEEKDAYS.map((label, value) => ({ value, label }));

// ---- Task actions -------------------------------------------------------

export interface TaskActionMeta {
  value: string;
  label: string;
}

/** Single source of truth for the action <select> and all label rendering. */
export const TASK_ACTIONS: TaskActionMeta[] = [
  { value: "command", label: "Run command" },
  { value: "restart", label: "Restart server" },
  { value: "stop", label: "Stop server" },
  { value: "backup", label: "Create backup" },
  { value: "mod_update", label: "Auto-update mods" },
];

const ACTION_LABELS: Record<string, string> = Object.fromEntries(
  TASK_ACTIONS.map((a) => [a.value, a.label]),
);

/** Human label for a task action, including the command when relevant. */
export function describeAction(
  action: string,
  payload?: Record<string, unknown> | null,
): string {
  const label = ACTION_LABELS[action] ?? action;
  if (action === "command") {
    const cmd = (payload?.command as string) ?? "";
    return cmd ? `${label}: ${cmd}` : label;
  }
  return label;
}
