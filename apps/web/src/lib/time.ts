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
