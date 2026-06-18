// Shared humanizing for audit actions, used by both the dashboard activity feed
// and the audit log page. Keeps the "names not IDs / severity / readable label"
// logic in one place.

import type { AuditEntry } from "@/lib/types";

export type AuditSeverity = "high" | "notice" | "info";

const LABELS: Record<string, string> = {
  "server.start": "Started server",
  "server.stop": "Stopped server",
  "server.restart": "Restarted server",
  "server.kill": "Killed server",
  "server.reinstall": "Reinstalled server",
  "server.crash": "Server crashed",
  "server.start_failed": "Server failed to start",
  "server.create": "Created server",
  "server.update": "Updated server config",
  "server.delete": "Deleted server",
  "mod.install": "Installed mod",
  "mod.uninstall": "Uninstalled mod",
  "mod.update": "Updated mod",
  "mod.disable_conflict": "Disabled conflicting mods",
  "mod.upload": "Uploaded custom mod",
  "backup.create": "Created backup",
  "backup.restore": "Restored backup",
  "backup.delete": "Deleted backup",
  "auth.login": "Signed in",
  "auth.logout": "Signed out",
};

/** Human-readable label for an audit action, falling back to a tidied id. */
export function actionLabel(action: string): string {
  if (LABELS[action]) return LABELS[action];
  // Fallback: "some.action_name" -> "Some action name".
  const tail = action.includes(".") ? action.split(".").slice(1).join(" ") : action;
  const text = tail.replace(/_/g, " ").trim();
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : action;
}

// Failures/crashes — the things an operator needs to notice and act on.
const HIGH_ACTIONS = new Set([
  "server.crash",
  "server.start_failed",
  "mod.autoupdate_failed",
  "mod.disable_conflict",
]);

// Successful but sensitive — destructive or security-relevant changes worth a
// second glance, but not an alarm.
const NOTICE_ACTIONS = new Set([
  "mod.uninstall",
  "mod.autoupdate_reverted",
  "server.reinstall",
  "node.delete",
  "user.delete",
]);

/** Severity used to color-code an action (high → red, notice → amber). */
export function actionSeverity(action: string): AuditSeverity {
  if (HIGH_ACTIONS.has(action)) return "high";
  if (NOTICE_ACTIONS.has(action) || action.endsWith(".delete")) return "notice";
  return "info";
}

/** The category prefix of an action, e.g. "server.start" -> "server". */
export function actionCategory(action: string): string {
  return action.includes(".") ? action.split(".")[0] : action;
}

// Low-signal actions that pile up (sign-ins/outs). Consecutive runs of these by
// the same actor are collapsed into a single summarized row in the audit log.
const COLLAPSIBLE = new Set(["auth.login", "auth.logout"]);

export function isCollapsible(action: string): boolean {
  return COLLAPSIBLE.has(action);
}

/** A single audit row or a collapsed run of repeated low-signal events. */
export interface AuditGroup {
  key: string;
  /** The representative action (all entries share it when collapsed). */
  action: string;
  /** Newest-first entries; length > 1 only for collapsed runs. */
  entries: AuditEntry[];
  collapsed: boolean;
}

/**
 * Collapse consecutive runs of the same collapsible action by the same actor
 * into one group. Input is assumed newest-first (as the API returns it); order
 * is preserved. Non-collapsible entries pass through as singleton groups.
 */
export function groupConsecutive(entries: AuditEntry[]): AuditGroup[] {
  const groups: AuditGroup[] = [];
  for (const e of entries) {
    const prev = groups[groups.length - 1];
    if (
      prev &&
      isCollapsible(e.action) &&
      prev.action === e.action &&
      prev.entries[0].user_id === e.user_id
    ) {
      prev.entries.push(e);
      prev.collapsed = true;
      continue;
    }
    groups.push({ key: String(e.id), action: e.action, entries: [e], collapsed: false });
  }
  return groups;
}
