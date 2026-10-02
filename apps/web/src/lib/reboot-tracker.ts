import { create } from "zustand";
import type { Node } from "./types";

// Follows one host reboot from "accepted" to "back", for the full-screen
// rebooting view. The decision logic is pure so it can be tested without
// timers; the store only remembers what is being tracked.
//
// "Back" means the host actually restarted, not merely that it answers: the
// node stays online while its servers are being stopped, so online alone would
// declare success before anything happened. A host's boot time is its
// last heartbeat minus its uptime, and both come from the same heartbeat on
// the server's clock. The reboot is done once that boot time is later than the
// server's own timestamp for the request (and than the boot time seen before
// it), so a wrong clock in the browser cannot fool it.

export type RebootPhase = "stopping" | "restarting" | "back";

type NodeSample = Pick<Node, "online" | "uptime_seconds" | "last_seen">;

export interface RebootObservation {
  /** Whether the API answered at all. False while the dashboard's own host is down. */
  reachable: boolean;
  /** The node as the API last saw it, or null when it could not say. */
  node: NodeSample | null;
}

/** Heartbeats are a second or so apart from the uptime they carry. */
const BOOT_TOLERANCE_MS = 2_000;
/** A boot time must move by more than heartbeat jitter to count as new. */
const NEW_BOOT_MARGIN_MS = 10_000;

/** When the node's host booted, by the server's clock, or null if unknown. */
export function bootTime(node: NodeSample | null | undefined): number | null {
  if (!node || node.uptime_seconds == null || !node.last_seen) return null;
  const seen = Date.parse(node.last_seen);
  if (Number.isNaN(seen)) return null;
  return seen - node.uptime_seconds * 1000;
}

/** Whether the host has booted since the reboot was requested. */
export function rebootedSince(
  node: NodeSample | null,
  requestedAt: string,
  previousBoot: number | null,
): boolean {
  if (!node?.online) return false;
  const booted = bootTime(node);
  if (booted === null) return false;
  const requested = Date.parse(requestedAt);
  if (Number.isNaN(requested) || booted <= requested - BOOT_TOLERANCE_MS) return false;
  return previousBoot === null || booted > previousBoot + NEW_BOOT_MARGIN_MS;
}

/** The phase after one more observation. Once the host has been seen going
 *  down it stays "restarting" until it is back, so a brief reachable blip
 *  while services start does not look like the servers stopping again. */
export function nextPhase(
  prev: RebootPhase,
  obs: RebootObservation,
  requestedAt: string,
  previousBoot: number | null,
): RebootPhase {
  if (prev === "back") return "back";
  if (obs.reachable && rebootedSince(obs.node, requestedAt, previousBoot)) return "back";
  if (!obs.reachable || (obs.node !== null && !obs.node.online)) return "restarting";
  return prev;
}

// ── What is being tracked ──────────────────────────────────────────────

export interface TrackedReboot {
  nodeId: string;
  nodeName: string;
  /** Server time the reboot was accepted (from the API response). */
  requestedAt: string;
  /** Boot time seen just before the request, when known. */
  previousBoot: number | null;
  /** Local time tracking started, for the elapsed-time display. */
  startedAt: number;
}

const STORAGE_KEY = "mcsm.reboot";
/** A tracked reboot older than this is stale and is not resumed on load. */
export const TRACKING_LIMIT_MS = 60 * 60 * 1000;

function load(): TrackedReboot | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const tracked = JSON.parse(raw) as TrackedReboot;
    if (!tracked?.nodeId || Date.now() - tracked.startedAt > TRACKING_LIMIT_MS) {
      localStorage.removeItem(STORAGE_KEY);
      return null;
    }
    return tracked;
  } catch {
    return null;
  }
}

interface RebootTrackerState {
  tracked: TrackedReboot | null;
  start: (node: Node, requestedAt: string) => void;
  clear: () => void;
}

// Persisted so a reload while a remote node restarts picks the screen back up.
// (When the dashboard's own host restarts, the page cannot reload until it is
// back anyway.)
export const useRebootTracker = create<RebootTrackerState>((set) => ({
  tracked: load(),
  start: (node, requestedAt) => {
    const tracked: TrackedReboot = {
      nodeId: node.id,
      nodeName: node.name,
      requestedAt,
      previousBoot: bootTime(node),
      startedAt: Date.now(),
    };
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(tracked));
    } catch {
      // Storage blocked: the screen still works, it just won't survive a reload.
    }
    set({ tracked });
  },
  clear: () => {
    try {
      localStorage.removeItem(STORAGE_KEY);
    } catch {
      // ignore
    }
    set({ tracked: null });
  },
}));
