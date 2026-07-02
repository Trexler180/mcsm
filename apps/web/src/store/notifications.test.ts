import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useNotifications } from "./notifications";

const toasts = () => useNotifications.getState().toasts;

describe("useNotifications", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    useNotifications.setState({ toasts: [] });
  });

  afterEach(() => {
    vi.runOnlyPendingTimers();
    vi.useRealTimers();
  });

  it("adds a toast with count 1", () => {
    useNotifications.getState().success("Mod installed");
    expect(toasts()).toHaveLength(1);
    expect(toasts()[0]).toMatchObject({
      title: "Mod installed",
      variant: "success",
      count: 1,
    });
  });

  it("merges same title+variant into one card with a bumped count", () => {
    const { success } = useNotifications.getState();
    success("Mod installed");
    success("Mod installed");
    success("Mod installed");
    expect(toasts()).toHaveLength(1);
    expect(toasts()[0].count).toBe(3);
  });

  it("keeps different variants as separate cards", () => {
    useNotifications.getState().success("Backup");
    useNotifications.getState().error("Backup");
    expect(toasts()).toHaveLength(2);
  });

  it("keeps different titles as separate cards", () => {
    useNotifications.getState().success("Mod installed");
    useNotifications.getState().success("Mod updated");
    expect(toasts()).toHaveLength(2);
  });

  it("auto-dismisses after the TTL", () => {
    useNotifications.getState().success("Bye");
    expect(toasts()).toHaveLength(1);
    vi.advanceTimersByTime(4000);
    expect(toasts()).toHaveLength(0);
  });

  it("re-arms the dismiss timer when a duplicate merges in", () => {
    const { success } = useNotifications.getState();
    success("Busy");
    vi.advanceTimersByTime(3000);
    success("Busy"); // merge at t=3s re-arms the 4s timer
    vi.advanceTimersByTime(3000); // t=6s — original timer would have fired
    expect(toasts()).toHaveLength(1);
    vi.advanceTimersByTime(1000); // t=7s — re-armed timer fires
    expect(toasts()).toHaveLength(0);
  });

  it("remove drops the card immediately", () => {
    useNotifications.getState().warning("Going away");
    const id = toasts()[0].id;
    useNotifications.getState().remove(id);
    expect(toasts()).toHaveLength(0);
  });
});
