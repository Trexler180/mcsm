// Shared, defensive time formatting. Guards against null / unparseable /
// epoch-zero / future timestamps so the UI never renders absurd durations like
// "739781d ago" (see the ops review).

function parse(iso: string | null | undefined): number | null {
  if (!iso) return null;
  const t = new Date(iso).getTime();
  if (Number.isNaN(t) || t <= 0) return null;
  return t;
}

/** Compact "x ago" relative time, or "never" when the timestamp is missing. */
export function relativeTime(iso: string | null | undefined): string {
  const t = parse(iso);
  if (t === null) return "never";
  const ms = Date.now() - t;
  if (ms < 0) return "just now";
  const min = Math.floor(ms / 60000);
  if (min < 1) return "just now";
  if (min < 60) return `${min}m ago`;
  const hrs = Math.floor(min / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  if (months < 12) return `${months}mo ago`;
  return `${Math.floor(months / 12)}y ago`;
}

/** Age of a timestamp in whole days, or null when missing/invalid. */
export function ageInDays(iso: string | null | undefined): number | null {
  const t = parse(iso);
  if (t === null) return null;
  return Math.floor((Date.now() - t) / 86_400_000);
}

/**
 * Compact duration since a unix-seconds timestamp ("3h 12m", "5d 4h"), or
 * null when missing / zero / in the future. Used for "up for X" labels fed by
 * the uptime tracker's online_since.
 */
export function durationSince(unixSeconds: number | null | undefined): string | null {
  if (!unixSeconds || unixSeconds <= 0) return null;
  const s = Math.floor(Date.now() / 1000) - unixSeconds;
  if (s < 0) return null;
  if (s < 60) return `${s}s`;
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
