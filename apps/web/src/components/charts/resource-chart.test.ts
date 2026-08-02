import { describe, expect, it } from "vitest";
import { appendTickSample } from "./resource-chart";

// The rules that keep the live tick series honest. The metrics stream ticks
// every 2s while the helper mod reports every 15s, so roughly six frames in
// seven carry a reading that is already plotted.
describe("appendTickSample", () => {
  it("appends the first reading", () => {
    expect(appendTickSample([], { tps: 19.98, tick_seq: 1 })).toEqual([
      { seq: 1, tps: 19.98 },
    ]);
  });

  it("ignores a frame restating the reading already plotted", () => {
    const first = appendTickSample([], { tps: 19.98, tick_seq: 4 });
    const again = appendTickSample(first, { tps: 19.98, tick_seq: 4 });

    // Same array back, not a copy: an unchanged reference is what stops React
    // re-rendering the chart six times between actual readings.
    expect(again).toBe(first);
  });

  it("appends when the mod has reported again", () => {
    let series = appendTickSample([], { tps: 20, tick_seq: 1 });
    series = appendTickSample(series, { tps: 20, tick_seq: 1 });
    series = appendTickSample(series, { tps: 18.4, tick_seq: 2 });

    expect(series).toEqual([
      { seq: 1, tps: 20 },
      { seq: 2, tps: 18.4 },
    ]);
  });

  it("appends a repeated value under a new sequence", () => {
    // A server holding steady at 20 TPS still produces a point per heartbeat —
    // deduping on the value rather than the sequence would flatten a healthy
    // server's line to a single point.
    let series = appendTickSample([], { tps: 20, tick_seq: 1 });
    series = appendTickSample(series, { tps: 20, tick_seq: 2 });

    expect(series).toHaveLength(2);
  });

  it("clears the series when a frame carries no tick data", () => {
    const series = appendTickSample([], { tps: 19.9, tick_seq: 1 });

    // The agent omits these fields once the last snapshot goes stale. Dropping
    // the series unmounts the card, which beats holding a line that stopped
    // meaning anything.
    expect(appendTickSample(series, {})).toEqual([]);
    expect(appendTickSample(series, { tps: 19.9 })).toEqual([]);
    expect(appendTickSample(series, { tick_seq: 2 })).toEqual([]);
  });

  it("stays referentially stable on a server that never reports", () => {
    // The overwhelmingly common case: no helper mod, so every 2s frame arrives
    // without tick fields. Handing back a fresh [] each time would re-render
    // the whole chart twice a minute per open dashboard, forever, to display
    // nothing.
    const empty: ReturnType<typeof appendTickSample> = [];
    expect(appendTickSample(empty, {})).toBe(empty);
  });

  it("keeps a zero reading rather than treating it as absent", () => {
    // 0 TPS is a frozen server — the single most important thing the graph can
    // show. A truthiness check here would discard exactly that reading.
    expect(appendTickSample([], { tps: 0, tick_seq: 3 })).toEqual([
      { seq: 3, tps: 0 },
    ]);
  });

  it("caps history at 60 points, keeping the newest", () => {
    let series: ReturnType<typeof appendTickSample> = [];
    for (let seq = 1; seq <= 75; seq++) {
      series = appendTickSample(series, { tps: 20, tick_seq: seq });
    }

    expect(series).toHaveLength(60);
    expect(series[0].seq).toBe(16);
    expect(series[series.length - 1].seq).toBe(75);
  });
});
