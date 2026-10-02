import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "@/lib/api";
import { useRebootTracker } from "@/lib/reboot-tracker";
import type { Node } from "@/lib/types";
import { POLL_INTERVAL_MS, RebootOverlay } from "./reboot-overlay";

const notify = { success: vi.fn(), error: vi.fn() };
vi.mock("@/store/notifications", () => ({ useNotifications: () => notify }));

// Before the reboot: up for a day as of 12:00:00.
const before = {
  id: "node-1",
  name: "host-1",
  online: true,
  last_seen: "2026-10-01T12:00:00Z",
  uptime_seconds: 86_400,
} as Node;
const requestedAt = "2026-10-01T12:00:05Z";
// Still answering while servers stop: same boot, later heartbeat.
const stopping = { ...before, last_seen: "2026-10-01T12:00:20Z", uptime_seconds: 86_420 } as Node;
// After the restart: seen at 12:03 having been up 40 seconds.
const rebooted = { ...before, last_seen: "2026-10-01T12:03:00Z", uptime_seconds: 40 } as Node;

function renderOverlay() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <RebootOverlay />
    </QueryClientProvider>,
  );
}

const poll = () =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
  });

describe("RebootOverlay", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    notify.success.mockReset();
    useRebootTracker.getState().clear();
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.restoreAllMocks();
    useRebootTracker.getState().clear();
  });

  it("renders nothing when no reboot is tracked", () => {
    renderOverlay();
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("follows the host down and back up, then reconnects on its own", async () => {
    const list = vi
      .spyOn(api.nodes, "list")
      .mockResolvedValueOnce([stopping])
      .mockRejectedValueOnce(new TypeError("Failed to fetch"))
      .mockRejectedValueOnce(new Error("HTTP 502"))
      .mockResolvedValueOnce([rebooted]);
    useRebootTracker.getState().start(before, requestedAt);
    renderOverlay();

    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getByText("Rebooting host-1")).toBeInTheDocument();

    await poll(); // still on the old boot
    expect(screen.getByText("Rebooting host-1")).toBeInTheDocument();

    await poll(); // the dashboard's own host is down
    expect(screen.getByText("Waiting for host-1 to come back")).toBeInTheDocument();
    expect(screen.getByText(/dashboard runs on this machine/i)).toBeInTheDocument();

    await poll(); // still down (proxy answers 502)
    expect(screen.getByText("Waiting for host-1 to come back")).toBeInTheDocument();

    await poll(); // booted after the request: done
    expect(list).toHaveBeenCalledTimes(4);
    expect(notify.success).toHaveBeenCalledWith("host-1 is back online", expect.any(String));
    expect(useRebootTracker.getState().tracked).toBeNull();
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  // The elapsed-time clock re-renders every second; the polling loop must keep
  // its own cadence regardless rather than being reset by each render.
  it("keeps polling at its own interval while the clock ticks", async () => {
    const list = vi.spyOn(api.nodes, "list").mockResolvedValue([stopping]);
    useRebootTracker.getState().start(before, requestedAt);
    renderOverlay();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS * 3 + 100);
    });
    expect(list).toHaveBeenCalledTimes(3);
    expect(screen.getByText("0:09")).toBeInTheDocument();
  });

  it("stops waiting when asked", async () => {
    vi.spyOn(api.nodes, "list").mockResolvedValue([stopping]);
    useRebootTracker.getState().start(before, requestedAt);
    renderOverlay();

    fireEvent.click(screen.getByRole("button", { name: /stop waiting/i }));
    expect(useRebootTracker.getState().tracked).toBeNull();
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });
});
