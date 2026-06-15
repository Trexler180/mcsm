export interface User {
  id: string;
  email: string;
  display_name: string | null;
  role: "admin" | "operator" | "user";
  created_at: string;
  last_login: string | null;
}

export interface Node {
  id: string;
  name: string;
  fqdn: string;
  port: number;
  scheme: string;
  memory_mb: number | null;
  disk_gb: number | null;
  cpu_cores: number | null;
  location: string | null;
  created_at: string;
  last_seen: string | null;
  online?: boolean;
}

export type ServerStatus =
  | "offline"
  | "starting"
  | "online"
  | "stopping"
  | "crashed"
  | "startup_failure"
  | "error";

export interface ConflictSuggestion {
  action: string; // "replace" | "remove" | "install"
  mod_id: string;
  mod_name: string;
  version?: string;
  requirements?: string[];
}

export interface ModConflict {
  detected: boolean;
  // "incompatible" = Fabric incompatible-mods block; "crash" = a mod (e.g. a
  // broken mixin) crashed the server on startup. Both are fixed by disabling
  // the named mod(s), so they share one dialog.
  kind?: "incompatible" | "crash";
  summary: string;
  suggestions: ConflictSuggestion[];
  raw: string[];
  detected_at: number;
}

export interface AgentStatus {
  status: string;
  pid?: number;
  mod_conflict?: ModConflict;
}

export interface Server {
  id: string;
  node_id: string;
  owner_id: string;
  name: string;
  description: string | null;
  platform: string;
  mc_version: string;
  loader_version: string | null;
  directory_path: string;
  java_binary: string;
  jvm_args: string[];
  port: number;
  ram_mb_min: number;
  ram_mb_max: number;
  status: ServerStatus;
  auto_start: boolean;
  tags: string[];
  settings: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface FileEntry {
  name: string;
  type: "file" | "dir";
  size: number;
  modified: string;
}

export interface FileListing {
  path: string;
  entries: FileEntry[];
}

export interface FileTreeEntry {
  path: string; // relative to the tree root, slash-separated
  type: "file" | "dir";
  size: number;
  modified: string;
}

export interface FileTree {
  path: string;
  entries: FileTreeEntry[];
  truncated: boolean;
}

export interface InstalledMod {
  id: string;
  server_id: string;
  source: string;
  source_id: string | null;
  version_id: string | null;
  name: string;
  version: string;
  file_name: string;
  sha256: string | null;
  pinned: boolean;
  enabled: boolean;
  install_path: string;
  installed_as_dep: boolean;
  installed_at: string;
  /** Names of installed mods that require this one (reverse deps). */
  required_by: string[];
  /** Auto-installed as a dependency that nothing needs anymore. */
  orphaned: boolean;
}

export type ModProjectType =
  | "mod"
  | "plugin"
  | "datapack"
  | "modpack"
  | "shader"
  | "resourcepack";

export type ModSortIndex =
  | "relevance"
  | "downloads"
  | "follows"
  | "newest"
  | "updated";

export type ModSource = "modrinth" | "curseforge" | "hangar" | "spigotmc";

export interface ModSearchParams {
  query: string;
  source?: ModSource;
  loader?: string;
  mcVersion?: string;
  projectType?: ModProjectType;
  categories?: string[];
  index?: ModSortIndex;
  /**
   * Environment facet: "server" (runs on server), "server_only" (server + client
   * unsupported), "client_server" (required on both), "client", "any", or ""
   * (server-relevant default).
   */
  environment?: string;
  limit?: number;
  offset?: number;
}

export interface ModUpdate {
  mod_id: string;
  name: string;
  current_version: string;
  latest_version: string;
  latest_version_id: string;
}

/** Per-mod outcome inside an auto-update run. */
export interface ModUpdateStep {
  mod_id: string;
  project_id: string;
  name: string;
  from_version: string;
  to_version: string;
  to_version_id: string;
  status: "pending" | "updated" | "reverted_skipped" | "failed";
  error?: string;
}

export interface ModUpdateRunDetail {
  phase:
    | "checking"
    | "applying"
    | "verifying"
    | "isolating"
    | "reverting"
    | "restoring"
    | "done";
  message?: string;
  was_running: boolean;
  mods: ModUpdateStep[];
}

export interface ModUpdateRun {
  id: string;
  server_id: string;
  trigger: string;
  status:
    | "running"
    | "success"
    | "no_updates"
    | "partial"
    | "reverted"
    | "failed";
  detail: ModUpdateRunDetail | null;
  started_at: string;
  finished_at: string | null;
}

/** A version the auto-updater blocklisted after it broke the server boot. */
export interface SkippedModVersion {
  server_id: string;
  project_id: string;
  version_id: string;
  mod_name: string;
  version: string;
  reason: string;
  created_at: string;
}

export interface GameVersion {
  version: string;
  stable: boolean;
}

export interface AuditEntry {
  id: number;
  user_id: string | null;
  server_id: string | null;
  action: string;
  detail: string | null;
  ip_address: string | null;
  created_at: string;
}

export interface IntegrationMeta {
  key: string;
  label: string;
  description: string;
  doc_url: string;
  configured: boolean;
  hint: string;
  updated_at?: string;
}

export interface ModSearchHit {
  project_id: string;
  slug: string;
  title: string;
  author: string;
  description: string;
  categories: string[];
  client_side: string;
  server_side: string;
  downloads: number;
  icon_url: string;
  versions: string[];
}

export interface ModCategory {
  /** CurseForge numeric category id used as the search filter value; absent for Modrinth (which filters by name). */
  id?: string;
  /** Raw inline SVG string (Modrinth) or an image URL (CurseForge). */
  icon: string;
  name: string;
  project_type: string;
  /** Group label, e.g. "categories", "features", "performance". */
  header: string;
}

export interface ModSearchResult {
  hits: ModSearchHit[];
  total_hits: number;
}

export interface ModVersion {
  id: string;
  project_id: string;
  name: string;
  version_number: string;
  version_type?: string;
  changelog?: string;
  game_versions: string[];
  loaders: string[];
  date_published: string;
}

export interface ModGalleryImage {
  url: string;
  title: string;
  description: string;
  featured: boolean;
}

export interface ModrinthProject {
  id: string;
  slug: string;
  title: string;
  description: string;
  body?: string;
  categories: string[];
  client_side: string;
  server_side: string;
  downloads: number;
  followers?: number;
  icon_url: string;
  project_type?: string;
  source_url?: string | null;
  issues_url?: string | null;
  wiki_url?: string | null;
  updated?: string;
  gallery?: ModGalleryImage[];
}

export interface Backup {
  id: string;
  server_id: string;
  target_id: string | null;
  triggered_by: string | null;
  trigger: string;
  status: "running" | "success" | "failed";
  size_bytes: number | null;
  snapshot_id: string | null;
  started_at: string;
  completed_at: string | null;
}

export interface BackupTarget {
  id: string;
  server_id: string;
  name: string;
  type: string;
  config: Record<string, unknown>;
  retention: Record<string, unknown> | null;
  is_default: boolean;
}

export interface Player {
  name: string;
  uuid?: string;
  online: boolean;
  /** Set for online players: when they joined this session. */
  joined_at?: string;
  /** Set for offline players: mtime of their playerdata .dat file. */
  last_seen?: string;
  /** Stamped from ops.json / whitelist.json / banned-players.json. */
  op?: boolean;
  op_level?: number;
  whitelisted?: boolean;
  banned?: boolean;
  ban_reason?: string;
  /** True for Bedrock Edition players bridged in via Geyser/Floodgate. */
  bedrock?: boolean;
}

/** Geyser/Floodgate (Bedrock bridge) status for a server. */
export interface GeyserInfo {
  installed: boolean;
  geyser: boolean;
  floodgate: boolean;
  /** Floodgate username prefix applied to Bedrock players (default "."). */
  prefix?: string;
}

/** A player administration action the backend can apply (live or offline). */
export type PlayerActionKind =
  | "op"
  | "deop"
  | "ban"
  | "pardon"
  | "kick"
  | "whitelist_add"
  | "whitelist_remove";

export interface Enchant {
  id: string; // e.g. "minecraft:sharpness"
  level: number;
}

export interface ItemStack {
  slot: number;
  id: string; // e.g. "minecraft:diamond_sword"
  count: number;
  /** Accumulated damage (durability used); absent for undamaged/unbreakable items. */
  damage?: number;
  /** Player-assigned (anvil) name, flattened to plain text. */
  custom_name?: string;
  enchantments?: Enchant[];
}

export interface PlayerStats {
  play_time_ticks?: number;
  deaths?: number;
  player_kills?: number;
  mob_kills?: number;
  jumps?: number;
  walked_cm?: number;
}

export interface PlayerDetail {
  name: string;
  uuid: string;
  online: boolean;
  health: number;
  max_health: number;
  food: number;
  xp_level: number;
  xp_total: number;
  game_mode: number;
  dimension: string;
  pos: number[]; // [x, y, z]
  score: number;
  selected_slot: number;
  inventory: ItemStack[];
  ender_chest: ItemStack[];
  /** mtime of the .dat file — the moment this snapshot was last saved to disk. */
  snapshot_at?: string;
  op?: boolean;
  whitelisted?: boolean;
  banned?: boolean;
  ban_reason?: string;
  bedrock?: boolean;
  stats?: PlayerStats | null;
}

export interface ScheduledTask {
  id: string;
  server_id: string;
  name: string;
  cron_expr: string;
  action: string;
  payload: Record<string, unknown> | null;
  enabled: boolean;
  last_run: string | null;
  next_run: string | null;
  created_at: string;
}

export interface LoginResponse {
  access_token: string;
  user: User;
}

export interface TokenResponse {
  access_token: string;
}
