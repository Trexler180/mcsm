import { describe, expect, it } from "vitest";
import {
  BUCKET_MS,
  MAX_POINTS,
  beginThroughput,
  etaSeconds,
  formatEta,
  formatRate,
  sampleThroughput,
} from "./upload-stats";

const MB = 1024 * 1024;

// Feed a steady stream of progress events at `eventMs` intervals, moving
// `bytesPerSecond` the whole way, as the browser would for an even connection.
function steadyUpload(bytesPerSecond: number, seconds: number, eventMs = 100) {
  let tp = beginThroughput(0);
  let loaded = 0;
  for (let t = eventMs; t <= seconds * 1000; t += eventMs) {
    loaded = (bytesPerSecond * t) / 1000;
    tp = sampleThroughput(tp, loaded, t);
  }
  return { tp, loaded };
}

describe("sampleThroughput", () => {
  it("returns the same object while a bucket is still open", () => {
    const start = beginThroughput(0);
    const next = sampleThroughput(start, 5000, BUCKET_MS - 1);
    // Identity, not just equality: React skips the re-render on an unchanged
    // reference, and these events arrive many times a second.
    expect(next).toBe(start);
  });

  it("records one point per bucket, not per progress event", () => {
    const { tp } = steadyUpload(2 * MB, 5, 50);
    expect(tp.history).toHaveLength((5 * 1000) / BUCKET_MS);
  });

  it("measures a steady connection at its actual speed", () => {
    const { tp } = steadyUpload(2 * MB, 10);
    expect(tp.bps).toBeCloseTo(2 * MB, -3);
    expect(tp.avgBps).toBeCloseTo(2 * MB, -3);
    expect(tp.peakBps).toBeCloseTo(2 * MB, -3);
  });

  it("reports the first bucket's speed immediately rather than ramping up", () => {
    let tp = beginThroughput(0);
    tp = sampleThroughput(tp, MB, 1000);
    expect(tp.bps).toBeCloseTo(MB, -3);
  });

  it("caps history and keeps the newest points", () => {
    const { tp } = steadyUpload(MB, (MAX_POINTS + 20) * (BUCKET_MS / 1000));
    expect(tp.history).toHaveLength(MAX_POINTS);
  });

  it("follows a slowdown without letting one bucket dominate", () => {
    let { tp } = steadyUpload(4 * MB, 10);
    const fast = tp.bps;
    // Connection drops to a crawl for one bucket.
    tp = sampleThroughput(tp, tp.lastLoaded + 1000, 10 * 1000 + BUCKET_MS);
    expect(tp.bps).toBeLessThan(fast);
    expect(tp.bps).toBeGreaterThan(0);
    // The peak is a high-water mark, so the stall must not erase it.
    expect(tp.peakBps).toBeCloseTo(fast, -3);
  });

  it("never reports a negative rate", () => {
    let tp = beginThroughput(0);
    tp = sampleThroughput(tp, 5 * MB, 1000);
    // Some browsers restate a smaller `loaded` after a retry.
    tp = sampleThroughput(tp, 4 * MB, 2000);
    expect(tp.history.every((v) => v >= 0)).toBe(true);
  });
});

describe("etaSeconds", () => {
  it("estimates from the smoothed speed", () => {
    const { tp, loaded } = steadyUpload(MB, 10);
    expect(etaSeconds(tp, loaded, loaded + 5 * MB)).toBeCloseTo(5, 0);
  });

  it("is null before a speed is known and once the bytes are all sent", () => {
    expect(etaSeconds(beginThroughput(0), 0, 100 * MB)).toBeNull();
    const { tp, loaded } = steadyUpload(MB, 5);
    expect(etaSeconds(tp, loaded, loaded)).toBeNull();
  });
});

describe("formatting", () => {
  it("quotes rates in byte units", () => {
    expect(formatRate(1.5 * MB)).toBe("1.5 MB/s");
    expect(formatRate(0)).toBe("—");
  });

  it("writes an eta people can read", () => {
    expect(formatEta(45)).toBe("45s");
    expect(formatEta(90)).toBe("1m 30s");
    expect(formatEta(120)).toBe("2m");
    expect(formatEta(0.4)).toBe("almost done");
    expect(formatEta(null)).toBe("—");
  });
});
