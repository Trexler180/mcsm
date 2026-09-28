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

  // A toast that asks the user something has to survive them looking away.
  it("does not auto-dismiss a sticky toast", () => {
    useNotifications.getState().add({
      title: "Steve tried to join",
      variant: "warning",
      sticky: true,
      action: { label: "Whitelist Steve", onClick: () => {} },
    });
    vi.advanceTimersByTime(60_000);
    expect(toasts()).toHaveLength(1);
  });

  // Two people knocking is two decisions. Merging them on a shared title would
  // silently drop one of them.
  it("never merges toasts that carry an action", () => {
    const add = useNotifications.getState().add;
    const action = { label: "Whitelist", onClick: () => {} };
    add({ title: "Player tried to join", variant: "warning", action });
    add({ title: "Player tried to join", variant: "warning", action });
    expect(toasts()).toHaveLength(2);
  });

  it("runAction marks the toast busy, then dismisses it", async () => {
    let release = () => {};
    const pending = new Promise<void>((resolve) => {
      release = resolve;
    });
    useNotifications.getState().add({
      title: "Steve tried to join",
      variant: "warning",
      sticky: true,
      action: { label: "Whitelist Steve", onClick: () => pending },
    });
    const id = toasts()[0].id;

    const run = useNotifications.getState().runAction(id);
    expect(toasts()[0].busy).toBe(true);

    release();
    await run;
    expect(toasts()).toHaveLength(0);
  });

  // An approval prompt has two real answers. Burying "deny" behind Dismiss
  // would make refusing look like ignoring, so both get a button.
  it("runs whichever of the two answers was pressed", async () => {
    const approve = vi.fn();
    const deny = vi.fn();
    useNotifications.getState().add({
      title: "Claude Code wants to restart survival",
      variant: "warning",
      sticky: true,
      action: { label: "Approve", onClick: approve },
      secondary: { label: "Deny", onClick: deny },
    });
    const id = toasts()[0].id;

    await useNotifications.getState().runAction(id, "secondary");
    expect(deny).toHaveBeenCalledOnce();
    expect(approve).not.toHaveBeenCalled();
    expect(toasts()).toHaveLength(0);
  });

  it("defaults to the primary answer when none is named", async () => {
    const approve = vi.fn();
    useNotifications.getState().add({
      title: "Prompt",
      variant: "warning",
      action: { label: "Approve", onClick: approve },
      secondary: { label: "Deny", onClick: vi.fn() },
    });
    await useNotifications.getState().runAction(toasts()[0].id);
    expect(approve).toHaveBeenCalledOnce();
  });

  // One busy flag for the whole toast: while an answer is in flight the other
  // answer must not also be pressable.
  it("refuses a second answer while the first is running", async () => {
    let release = () => {};
    const pending = new Promise<void>((resolve) => {
      release = resolve;
    });
    const deny = vi.fn();
    useNotifications.getState().add({
      title: "Prompt",
      variant: "warning",
      sticky: true,
      action: { label: "Approve", onClick: () => pending },
      secondary: { label: "Deny", onClick: deny },
    });
    const id = toasts()[0].id;

    const run = useNotifications.getState().runAction(id, "action");
    await useNotifications.getState().runAction(id, "secondary");
    expect(deny).not.toHaveBeenCalled();

    release();
    await run;
  });

  // A request answered on another device, or one that lapsed, must not leave a
  // live-looking button behind.
  it("removes a keyed toast and ignores an unknown key", () => {
    useNotifications.getState().add({
      title: "Prompt",
      variant: "warning",
      sticky: true,
      key: "mcp-action:req-1",
      action: { label: "Approve", onClick: () => {} },
    });
    useNotifications.getState().removeByKey("mcp-action:other");
    expect(toasts()).toHaveLength(1);
    useNotifications.getState().removeByKey("mcp-action:req-1");
    expect(toasts()).toHaveLength(0);
  });

  it("dismisses the toast even when its action fails", async () => {
    useNotifications.getState().add({
      title: "Steve tried to join",
      variant: "warning",
      sticky: true,
      action: {
        label: "Whitelist Steve",
        onClick: () => Promise.reject(new Error("server said no")),
      },
    });
    const id = toasts()[0].id;
    await expect(useNotifications.getState().runAction(id)).rejects.toThrow(
      "server said no",
    );
    expect(toasts()).toHaveLength(0);
  });
});
