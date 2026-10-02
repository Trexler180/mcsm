import { describe, expect, it } from "vitest";
import { bootTime, nextPhase, rebootedSince } from "./reboot-tracker";

const requestedAt = "2026-10-01T12:00:00Z";
const at = (iso: string) => Date.parse(iso);

// A heartbeat: seen at `seen`, having been up `uptime` seconds.
const node = (seen: string, uptime: number, online = true) => ({
  online,
  last_seen: seen,
  uptime_seconds: uptime,
});

describe("bootTime", () => {
  it("is the heartbeat time minus uptime", () => {
    expect(bootTime(node("2026-10-01T12:00:00Z", 60))).toBe(at("2026-10-01T11:59:00Z"));
  });
  it("is unknown without a heartbeat", () => {
    expect(bootTime({ online: false, last_seen: null, uptime_seconds: null })).toBeNull();
    expect(bootTime(null)).toBeNull();
  });
});

describe("rebootedSince", () => {
  const before = at("2026-09-20T00:00:00Z"); // up for days before the request

  it("is false while the host is still on its old boot", () => {
    // Still answering while servers stop: online, but booted days ago.
    expect(rebootedSince(node("2026-10-01T12:00:30Z", 1_000_000), requestedAt, before)).toBe(false);
  });

  it("is true once the host booted after the request", () => {
    // Seen at 12:03 having been up 60s: booted 12:02, after the 12:00 request.
    expect(rebootedSince(node("2026-10-01T12:03:00Z", 60), requestedAt, before)).toBe(true);
  });

  it("needs the node online", () => {
    expect(rebootedSince(node("2026-10-01T12:03:00Z", 60, false), requestedAt, before)).toBe(false);
  });

  it("is not fooled by a host that had itself only just booted", () => {
    // The host booted at 11:59:59, a second before the request. Within the
    // clock tolerance of the request, so only the previous-boot check stops a
    // false "back" here.
    const previous = at("2026-10-01T11:59:59Z");
    expect(rebootedSince(node("2026-10-01T12:00:20Z", 21), requestedAt, previous)).toBe(false);
    // Its next real boot still counts.
    expect(rebootedSince(node("2026-10-01T12:03:00Z", 30), requestedAt, previous)).toBe(true);
  });
});

describe("nextPhase", () => {
  const before = at("2026-09-20T00:00:00Z");
  const stillUp = { reachable: true, node: node("2026-10-01T12:00:30Z", 1_000_000) };

  it("stays on stopping while the old boot keeps answering", () => {
    expect(nextPhase("stopping", stillUp, requestedAt, before)).toBe("stopping");
  });

  it("moves to restarting when the dashboard cannot be reached", () => {
    expect(nextPhase("stopping", { reachable: false, node: null }, requestedAt, before)).toBe("restarting");
  });

  it("moves to restarting when a remote node goes offline", () => {
    const offline = { reachable: true, node: node("2026-10-01T12:00:30Z", 1_000_000, false) };
    expect(nextPhase("stopping", offline, requestedAt, before)).toBe("restarting");
  });

  it("does not fall back to stopping on a reachable blip mid-restart", () => {
    expect(nextPhase("restarting", stillUp, requestedAt, before)).toBe("restarting");
  });

  it("is back once the host has booted again, even if the outage was never seen", () => {
    const rebooted = { reachable: true, node: node("2026-10-01T12:03:00Z", 60) };
    expect(nextPhase("stopping", rebooted, requestedAt, before)).toBe("back");
    expect(nextPhase("restarting", rebooted, requestedAt, before)).toBe("back");
  });
});
