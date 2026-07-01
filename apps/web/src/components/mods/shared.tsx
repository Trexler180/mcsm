import type {
  ModProjectType,
  ModSearchHit,
  ModSortIndex,
  ModVersion,
} from "@/lib/types";

export const PROJECT_TYPES: { value: ModProjectType; label: string }[] = [
  { value: "mod", label: "Mods" },
  { value: "plugin", label: "Plugins" },
  { value: "datapack", label: "Datapacks" },
  { value: "modpack", label: "Modpacks" },
  { value: "shader", label: "Shaders" },
  { value: "resourcepack", label: "Resource Packs" },
];

export const SORTS: { value: ModSortIndex; label: string }[] = [
  { value: "relevance", label: "Relevance" },
  { value: "downloads", label: "Downloads" },
  { value: "follows", label: "Follows" },
  { value: "newest", label: "Newest" },
  { value: "updated", label: "Updated" },
];

// Loaders selectable when browsing mods. Mirrors the Modrinth loader facets.
export const LOADERS = [
  "fabric",
  "forge",
  "quilt",
  "neoforge",
  "paper",
  "spigot",
  "purpur",
  "bukkit",
];

export const PAGE_SIZE = 20;

export function sourceBadgeClass(source: string): string {
  switch (source) {
    case "modrinth":
      return "bg-emerald-500/15 text-emerald-400 border-emerald-500/30";
    case "curseforge":
      return "bg-orange-500/15 text-orange-400 border-orange-500/30";
    case "hangar":
      return "bg-sky-500/15 text-sky-400 border-sky-500/30";
    case "spigotmc":
      return "bg-yellow-500/15 text-yellow-400 border-yellow-500/30";
    default:
      return "bg-surface-2 text-text-secondary border-border/50";
  }
}

// environmentTag derives a side chip from Modrinth's client_side/server_side
// metadata. CurseForge doesn't expose sides, so its hits carry empty values
// and get no tag.
export function environmentTag(
  clientSide?: string,
  serverSide?: string,
): { label: string; className: string; title: string } | null {
  const onServer = serverSide === "required" || serverSide === "optional";
  const onClient = clientSide === "required" || clientSide === "optional";
  if (onServer && clientSide === "unsupported") {
    return {
      label: "server only",
      className: "bg-blue-500/15 text-blue-400 border-blue-500/30",
      title: "Runs on the server only — players don't need to install it",
    };
  }
  if (onServer) {
    return {
      label: "server any",
      className: "bg-cyan-500/15 text-cyan-400 border-cyan-500/30",
      title: "Runs on the server; also used or needed on the client",
    };
  }
  if (onClient && serverSide === "unsupported") {
    return {
      label: "client only",
      className: "bg-rose-500/15 text-rose-400 border-rose-500/30",
      title: "Client-side only — does nothing when installed on a server",
    };
  }
  return null;
}

export function formatDownloads(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(0)}K`;
  return String(n);
}

// compatible reports whether a search hit can install onto the server without a
// forced override. Modrinth lists loader names inside hit.categories; CurseForge
// does not, so callers pass an empty serverLoader for CF to skip the loader half.
// Sources without reliable per-version data (SpigotMC) pass an empty serverMc;
// hits with no version list at all also skip the MC gate.
export function compatible(
  hit: ModSearchHit,
  serverLoader: string,
  serverMc: string,
  projectType: ModProjectType,
): boolean {
  const vers = hit.versions ?? [];
  if (serverMc && vers.length > 0 && !vers.includes(serverMc)) return false;
  if (projectType === "mod" && serverLoader && !(hit.categories ?? []).includes(serverLoader))
    return false;
  return true;
}

export function formatVersionDate(date?: string): string {
  if (!date) return "";
  return new Date(date).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

export function versionCompatible(
  version: ModVersion,
  serverLoader: string,
  serverMc: string,
): boolean {
  const loaders = version.loaders.map((l) => l.toLowerCase());
  if (serverMc && !version.game_versions.includes(serverMc)) return false;
  if (serverLoader && loaders.length > 0 && !loaders.includes(serverLoader.toLowerCase()))
    return false;
  return true;
}

export function versionTypeClass(type?: string): string {
  switch (type) {
    case "release":
      return "border-green-500/50 bg-green-500/15 text-green-400";
    case "beta":
      return "border-amber-500/50 bg-amber-500/15 text-amber-400";
    case "alpha":
      return "border-red-500/50 bg-red-500/15 text-red-400";
    default:
      return "border-border bg-surface-2 text-text-secondary";
  }
}
