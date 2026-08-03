import { useCallback, useRef, useState } from "react";
import type { UploadProgress } from "@/lib/api";

// How often a throughput point is recorded, and how many are kept. Browsers
// fire upload progress events far faster than this and at an irregular cadence,
// so speed is measured over fixed buckets instead: dividing bytes by the gap
// between two arbitrary events yields a number that swings wildly for reasons
// that have nothing to do with the connection.
export const BUCKET_MS = 500;
export const MAX_POINTS = 60; // 30s of history
/** Width of the graph's time axis, in seconds. */
export const WINDOW_SECONDS = (BUCKET_MS * MAX_POINTS) / 1000;

// Weight of each new bucket in the headline speed. Raw bucket speeds are jumpy
// enough that an unsmoothed readout is unreadable — it changes faster than it
// can be read — while too much smoothing hides a genuine stall.
const SMOOTHING = 0.4;

export interface Throughput {
  /** Bytes per second, exponentially smoothed. 0 until the first full bucket. */
  bps: number;
  /** Fastest bucket seen this upload. */
  peakBps: number;
  /** Bytes per second averaged over the whole upload so far. */
  avgBps: number;
  /** One entry per bucket, oldest first, capped at MAX_POINTS. */
  history: number[];
  startedAt: number;
  /** End of the most recent completed bucket. */
  lastAt: number;
  lastLoaded: number;
}

export function beginThroughput(now: number): Throughput {
  return {
    bps: 0,
    peakBps: 0,
    avgBps: 0,
    history: [],
    startedAt: now,
    lastAt: now,
    lastLoaded: 0,
  };
}

/**
 * Folds one progress reading into the series, closing off a bucket when enough
 * time has passed.
 *
 * Returns the previous state unchanged when the bucket is still open, so React
 * bails out of the re-render — the same reason appendTickSample in the resource
 * chart does it, and it matters more here: progress events for a large upload
 * arrive many times a second for minutes.
 */
export function sampleThroughput(
  prev: Throughput,
  loaded: number,
  now: number,
): Throughput {
  const elapsed = now - prev.lastAt;
  if (elapsed < BUCKET_MS) return prev;

  const bucketBps = ((loaded - prev.lastLoaded) / elapsed) * 1000;
  const history =
    prev.history.length === MAX_POINTS ? prev.history.slice(1) : prev.history.slice();
  history.push(Math.max(0, bucketBps));

  const totalElapsed = now - prev.startedAt;
  return {
    // The first bucket has nothing to smooth against, so it seeds the average
    // outright; smoothing from zero would otherwise spend several seconds
    // climbing to a speed that was correct from the start.
    bps: prev.bps === 0 ? bucketBps : prev.bps + (bucketBps - prev.bps) * SMOOTHING,
    peakBps: Math.max(prev.peakBps, bucketBps),
    avgBps: totalElapsed > 0 ? (loaded / totalElapsed) * 1000 : 0,
    history,
    startedAt: prev.startedAt,
    lastAt: now,
    lastLoaded: loaded,
  };
}

/**
 * Seconds left at the current smoothed speed, or null when there isn't enough
 * to go on. Deliberately based on the smoothed speed rather than the average:
 * on a connection that has just slowed down, the average keeps promising a
 * finish time that stopped being true a while ago.
 */
export function etaSeconds(
  tp: Throughput,
  loaded: number,
  total: number,
): number | null {
  if (tp.bps <= 0 || total <= 0 || loaded >= total) return null;
  return (total - loaded) / tp.bps;
}

const SIZE_UNITS = ["B", "KB", "MB", "GB"];

export function formatBytes(bytes: number): string {
  let i = 0;
  let v = bytes;
  while (v >= 1024 && i < SIZE_UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${SIZE_UNITS[i]}`;
}

/** Transfer rate in the units people quote connections in — MB/s, not Mbps. */
export function formatRate(bps: number): string {
  if (!Number.isFinite(bps) || bps <= 0) return "—";
  return `${formatBytes(bps)}/s`;
}

export function formatEta(seconds: number | null): string {
  if (seconds === null || !Number.isFinite(seconds)) return "—";
  if (seconds < 1) return "almost done";
  if (seconds < 60) return `${Math.ceil(seconds)}s`;
  const mins = Math.floor(seconds / 60);
  const secs = Math.round(seconds % 60);
  if (mins < 60) return secs === 0 ? `${mins}m` : `${mins}m ${secs}s`;
  return `${Math.floor(mins / 60)}h ${mins % 60}m`;
}

export interface UploadStats {
  loaded: number;
  total: number;
  /** 0-100, rounded. */
  percent: number;
  throughput: Throughput;
  etaSec: number | null;
}

/**
 * Tracks bytes, speed and history for one upload.
 *
 * `onProgress` is handed straight to the api client; `begin` resets between
 * attempts, and `end` keeps the final numbers on screen for the phase after the
 * bytes land (a world still has to be unpacked on the node) rather than
 * blanking the panel the moment the upload finishes.
 */
export function useUploadStats() {
  const [stats, setStats] = useState<UploadStats | null>(null);
  const tp = useRef<Throughput>(beginThroughput(0));

  const begin = useCallback(() => {
    tp.current = beginThroughput(Date.now());
    setStats({
      loaded: 0,
      total: 0,
      percent: 0,
      throughput: tp.current,
      etaSec: null,
    });
  }, []);

  const onProgress = useCallback((p: UploadProgress) => {
    tp.current = sampleThroughput(tp.current, p.loaded, Date.now());
    setStats({
      loaded: p.loaded,
      total: p.total,
      percent: p.total > 0 ? Math.min(100, Math.round((p.loaded / p.total) * 100)) : 0,
      throughput: tp.current,
      etaSec: etaSeconds(tp.current, p.loaded, p.total),
    });
  }, []);

  const clear = useCallback(() => setStats(null), []);

  return { stats, begin, onProgress, clear };
}
