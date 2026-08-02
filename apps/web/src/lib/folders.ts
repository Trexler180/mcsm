import type { FolderColor, ServerFolder } from "./types";

/**
 * Folder grouping shared by the servers list and the dashboard fleet grid, so
 * both render the same sections in the same order from the same rules.
 */

/** The minimum a server needs to be groupable — Server and OverviewServer both fit. */
export type Groupable = { folder_id: string | null };

export type FolderGroup<T extends Groupable> = {
  /** null is the trailing "Ungrouped" section. */
  folder: ServerFolder | null;
  servers: T[];
};

/**
 * Buckets servers into their folders, preserving the folder order the API sent
 * (sort_order, then name) and appending ungrouped servers last.
 *
 * Empty folders are kept so an admin can see the folder they just made and drop
 * servers into it. A server pointing at a folder the caller can't see — possible
 * when a collaborator sees one server out of a folder they don't otherwise have
 * access to — falls into Ungrouped rather than vanishing from the list.
 */
export function groupServersByFolder<T extends Groupable>(
  servers: T[],
  folders: ServerFolder[],
): FolderGroup<T>[] {
  const groups = new Map<string, FolderGroup<T>>();
  for (const folder of folders) {
    groups.set(folder.id, { folder, servers: [] });
  }

  const ungrouped: T[] = [];
  for (const server of servers) {
    const group = server.folder_id ? groups.get(server.folder_id) : undefined;
    if (group) group.servers.push(server);
    else ungrouped.push(server);
  }

  const result = [...groups.values()];
  if (ungrouped.length > 0) {
    result.push({ folder: null, servers: ungrouped });
  }
  return result;
}

/**
 * Tailwind classes per folder accent. Written out in full rather than
 * interpolated so the compiler can see every class it needs to emit.
 */
const ACCENTS: Record<FolderColor, { dot: string; chip: string }> = {
  "": { dot: "bg-text-secondary", chip: "border-border bg-surface-2 text-text-secondary" },
  slate: { dot: "bg-slate-400", chip: "border-slate-500/30 bg-slate-500/10 text-slate-300" },
  blue: { dot: "bg-blue-400", chip: "border-blue-500/30 bg-blue-500/10 text-blue-300" },
  green: { dot: "bg-green-400", chip: "border-green-500/30 bg-green-500/10 text-green-300" },
  amber: { dot: "bg-amber-400", chip: "border-amber-500/30 bg-amber-500/10 text-amber-300" },
  red: { dot: "bg-red-400", chip: "border-red-500/30 bg-red-500/10 text-red-300" },
  purple: { dot: "bg-purple-400", chip: "border-purple-500/30 bg-purple-500/10 text-purple-300" },
  pink: { dot: "bg-pink-400", chip: "border-pink-500/30 bg-pink-500/10 text-pink-300" },
  cyan: { dot: "bg-cyan-400", chip: "border-cyan-500/30 bg-cyan-500/10 text-cyan-300" },
};

/** Palette offered in the folder editor; must match the API's allowed set. */
export const FOLDER_COLORS: FolderColor[] = [
  "",
  "slate",
  "blue",
  "green",
  "amber",
  "red",
  "purple",
  "pink",
  "cyan",
];

/** Accent classes for a folder color, falling back to the neutral default. */
export function folderAccent(color: FolderColor | string | undefined) {
  return ACCENTS[(color ?? "") as FolderColor] ?? ACCENTS[""];
}
