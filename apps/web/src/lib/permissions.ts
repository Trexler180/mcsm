import type {
  MyServerPermissions,
  ServerPermission,
  ServerPermissionGroup,
} from "./types";

export interface PermissionLeafDef {
  value: ServerPermission;
  label: string;
}

export interface PermissionGroupDef {
  group: ServerPermissionGroup;
  label: string;
  detail: string;
  // Empty for atomic groups (view, console, tasks, settings, admin).
  leaves: PermissionLeafDef[];
}

// The permission model, kept in lockstep with the API's store package. Order
// here drives both display order and the canonical sort.
export const PERMISSION_MODEL: PermissionGroupDef[] = [
  { group: "view", label: "View", detail: "Open the dashboard, status, logs", leaves: [] },
  {
    group: "power",
    label: "Power",
    detail: "Control the server lifecycle",
    leaves: [
      { value: "power.start", label: "Start" },
      { value: "power.stop", label: "Stop" },
      { value: "power.restart", label: "Restart" },
      { value: "power.kill", label: "Kill" },
    ],
  },
  { group: "console", label: "Console", detail: "Open console and send commands", leaves: [] },
  {
    group: "players",
    label: "Players",
    detail: "Manage players on the server",
    leaves: [
      { value: "players.whitelist", label: "Whitelist add / remove" },
      { value: "players.kick", label: "Kick" },
      { value: "players.ban", label: "Ban / pardon (players & IPs)" },
      { value: "players.op", label: "Op / deop" },
    ],
  },
  {
    group: "files",
    label: "Files",
    detail: "Server files and worlds",
    leaves: [
      { value: "files.read", label: "Read / download" },
      { value: "files.write", label: "Edit / upload" },
      { value: "files.delete", label: "Delete" },
    ],
  },
  {
    group: "mods",
    label: "Mods",
    detail: "Mods and modpacks",
    leaves: [
      { value: "mods.install", label: "Install" },
      { value: "mods.update", label: "Update / toggle" },
      { value: "mods.remove", label: "Remove" },
    ],
  },
  {
    group: "backups",
    label: "Backups",
    detail: "Server backups",
    leaves: [
      { value: "backups.create", label: "Create" },
      { value: "backups.restore", label: "Restore" },
      { value: "backups.delete", label: "Delete" },
    ],
  },
  { group: "tasks", label: "Tasks", detail: "Manage scheduled tasks", leaves: [] },
  { group: "settings", label: "Settings", detail: "Edit options and reinstall", leaves: [] },
  { group: "admin", label: "Manage access", detail: "Manage members and delete server", leaves: [] },
];

export const ALL_PERMISSIONS: ServerPermission[] = PERMISSION_MODEL.flatMap(
  (g) => [g.group, ...g.leaves.map((l) => l.value)],
);

const ORDER = new Map<ServerPermission, number>(
  ALL_PERMISSIONS.map((p, i) => [p, i]),
);

export function permissionParent(
  p: ServerPermission,
): ServerPermissionGroup | null {
  const i = p.indexOf(".");
  return i >= 0 ? (p.slice(0, i) as ServerPermissionGroup) : null;
}

export function sortPerms(perms: ServerPermission[]): ServerPermission[] {
  return [...new Set(perms)].sort(
    (a, b) => (ORDER.get(a) ?? 999) - (ORDER.get(b) ?? 999),
  );
}

// collapsePerms mirrors the backend's NormalizeServerPermissions: a leaf whose
// parent group is also selected is redundant and dropped. Keeping the client in
// sync means the optimistic-concurrency check never trips on cosmetic diffs.
export function collapsePerms(perms: ServerPermission[]): ServerPermission[] {
  const set = new Set(perms);
  return sortPerms(
    [...set].filter((p) => {
      const parent = permissionParent(p);
      return !(parent && set.has(parent));
    }),
  );
}

export function samePerms(
  a: ServerPermission[],
  b: ServerPermission[],
): boolean {
  const aa = collapsePerms(a);
  const bb = collapsePerms(b);
  return aa.length === bb.length && aa.every((p, i) => p === bb[i]);
}

// can mirrors the API's authorization: a leaf is satisfied by the leaf itself or
// its parent group; a group is satisfied by the group or any of its leaves
// (read-level access). Any non-empty grant implies view.
export function can(
  my: MyServerPermissions | undefined,
  needed: ServerPermission,
): boolean {
  if (!my) return needed === "view";
  if (my.owner || my.global_admin) return true;
  const held = my.permissions;
  if (held.includes("admin")) return true;
  if (needed === "view" && held.length > 0) return true;
  if (held.includes(needed)) return true;
  const parent = permissionParent(needed);
  if (parent) return held.includes(parent);
  return held.some((p) => permissionParent(p) === needed);
}
