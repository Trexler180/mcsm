// Shared humanizing for audit actions, used by both the dashboard activity feed
// and the audit log page. Keeps the "names not IDs / severity / readable label"
// logic in one place.

export type AuditSeverity = "high" | "info";

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

/** Severity used to color-code an action (crashes/failures stand out). */
export function actionSeverity(action: string): AuditSeverity {
  if (
    action === "server.crash" ||
    action === "server.start_failed" ||
    action.startsWith("mod.disable_conflict")
  ) {
    return "high";
  }
  return "info";
}

/** The category prefix of an action, e.g. "server.start" -> "server". */
export function actionCategory(action: string): string {
  return action.includes(".") ? action.split(".")[0] : action;
}
