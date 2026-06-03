import { useEffect, useMemo, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  Search,
  Package,
  Trash2,
  Download,
  Loader2,
  Pin,
  PinOff,
  ArrowUpCircle,
  Unlink,
  Compass,
  ArrowRightLeft,
  AlertTriangle,
  FileText,
  X,
  RefreshCw,
  ArrowDownAZ,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ConfirmDialog, Dialog } from "@/components/ui/dialog";
import { ModDetailDialog } from "@/components/mods/detail";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type {
  InstalledMod,
  ModProjectType,
  ModSearchHit,
  ModSortIndex,
  ModSource,
  ModUpdate,
  ModVersion,
} from "@/lib/types";

interface ModSearchProps {
  serverId: string;
  /** Server platform, doubles as the Modrinth loader facet for mods. */
  loader: string;
  mcVersion: string;
  /** Server platform, used to fetch the Minecraft version list. */
  platform: string;
}

const PROJECT_TYPES: { value: ModProjectType; label: string }[] = [
  { value: "mod", label: "Mods" },
  { value: "plugin", label: "Plugins" },
  { value: "datapack", label: "Datapacks" },
  { value: "modpack", label: "Modpacks" },
  { value: "shader", label: "Shaders" },
  { value: "resourcepack", label: "Resource Packs" },
];

const SORTS: { value: ModSortIndex; label: string }[] = [
  { value: "relevance", label: "Relevance" },
  { value: "downloads", label: "Downloads" },
  { value: "follows", label: "Follows" },
  { value: "newest", label: "Newest" },
  { value: "updated", label: "Updated" },
];

// Loaders selectable when browsing mods. Mirrors the Modrinth loader facets.
const LOADERS = [
  "fabric",
  "forge",
  "quilt",
  "neoforge",
  "paper",
  "spigot",
  "purpur",
  "bukkit",
];

const PAGE_SIZE = 20;

type BulkUpdateProgress = {
  total: number;
  completed: number;
  currentName: string;
};

function formatDownloads(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(0)}K`;
  return String(n);
}

// compatible reports whether a search hit can install onto the server without a
// forced override. Modrinth lists loader names inside hit.categories; CurseForge
// does not, so callers pass an empty serverLoader for CF to skip the loader half.
function compatible(
  hit: ModSearchHit,
  serverLoader: string,
  serverMc: string,
  projectType: ModProjectType,
): boolean {
  if (serverMc && !hit.versions.includes(serverMc)) return false;
  if (projectType === "mod" && serverLoader && !hit.categories.includes(serverLoader))
    return false;
  return true;
}

// Toggle switch styled after the Modrinth desktop app's enable/disable control.
function Switch({
  checked,
  onChange,
  disabled,
  title,
}: {
  checked: boolean;
  onChange: () => void;
  disabled?: boolean;
  title?: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={onChange}
      title={title}
      className={`relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors disabled:opacity-50 disabled:cursor-not-allowed ${
        checked ? "bg-accent" : "bg-surface-2 border border-border"
      }`}
    >
      <span
        className={`inline-block h-3.5 w-3.5 transform rounded-full transition-transform ${
          checked ? "translate-x-[1.125rem] bg-black" : "translate-x-1 bg-text-secondary"
        }`}
      />
    </button>
  );
}

function formatVersionDate(date?: string): string {
  if (!date) return "";
  return new Date(date).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

function versionCompatible(
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

function versionTypeClass(type?: string): string {
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

function switchActionLabel(
  selected: ModVersion | null,
  current: ModVersion | null,
  versions: ModVersion[],
): string {
  if (!selected) return "Switch";
  if (current && selected.id === current.id) return "Current version";
  if (!current) return `Switch to ${selected.version_number}`;

  const selectedIndex = versions.findIndex((v) => v.id === selected.id);
  const currentIndex = versions.findIndex((v) => v.id === current.id);
  if (selectedIndex >= 0 && currentIndex >= 0) {
    return selectedIndex < currentIndex
      ? `Update to ${selected.version_number}`
      : `Downgrade to ${selected.version_number}`;
  }

  const selectedDate = Date.parse(selected.date_published);
  const currentDate = Date.parse(current.date_published);
  if (!Number.isNaN(selectedDate) && !Number.isNaN(currentDate)) {
    return selectedDate > currentDate
      ? `Update to ${selected.version_number}`
      : `Downgrade to ${selected.version_number}`;
  }

  return `Switch to ${selected.version_number}`;
}

const changelogComponents = {
  h1: (p: any) => <h1 className="text-lg font-semibold text-text-primary mt-4 mb-2" {...p} />,
  h2: (p: any) => <h2 className="text-base font-semibold text-text-primary mt-4 mb-2" {...p} />,
  h3: (p: any) => <h3 className="text-sm font-semibold text-text-primary mt-3 mb-1.5" {...p} />,
  p: (p: any) => <p className="text-sm text-text-secondary leading-relaxed my-2" {...p} />,
  a: (p: any) => (
    <a className="text-accent hover:underline" target="_blank" rel="noreferrer noopener" {...p} />
  ),
  ul: (p: any) => <ul className="list-disc pl-5 my-2 text-sm text-text-secondary space-y-1" {...p} />,
  ol: (p: any) => <ol className="list-decimal pl-5 my-2 text-sm text-text-secondary space-y-1" {...p} />,
  code: (p: any) => (
    <code className="px-1 py-0.5 rounded bg-surface-2 text-xs font-mono text-text-primary" {...p} />
  ),
  pre: (p: any) => (
    <pre className="p-3 rounded bg-surface-2 overflow-x-auto text-xs my-2" {...p} />
  ),
};

function VersionSwitchDialog({
  open,
  onClose,
  serverId,
  mod,
  loader,
  mcVersion,
}: {
  open: boolean;
  onClose: () => void;
  serverId: string;
  mod: InstalledMod | null;
  loader: string;
  mcVersion: string;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [selectedId, setSelectedId] = useState("");
  const [searchTerm, setSearchTerm] = useState("");
  const [showIncompatible, setShowIncompatible] = useState(false);

  const projectId = mod?.source_id ?? "";
  const source = mod?.source as ModSource | undefined;

  const { data: versions = [], isFetching } = useQuery({
    queryKey: ["mod-switch-versions", serverId, source, projectId],
    queryFn: () => api.mods.getVersions(serverId, projectId, "", "", source),
    enabled: open && !!projectId && !!source,
    staleTime: 10 * 60_000,
  });

  const { data: project } = useQuery({
    queryKey: ["mod-switch-project", serverId, source, projectId],
    queryFn: () => api.mods.getProject(serverId, projectId, source),
    enabled: open && !!projectId && !!source,
    staleTime: 10 * 60_000,
  });

  useEffect(() => {
    if (!open) return;
    setSelectedId(mod?.version_id ?? "");
    setSearchTerm("");
    setShowIncompatible(false);
  }, [open, mod?.id, mod?.version_id]);

  const compatibleVersions = useMemo(
    () => versions.filter((v) => versionCompatible(v, loader, mcVersion)),
    [versions, loader, mcVersion],
  );

  const visibleVersions = useMemo(() => {
    const base = showIncompatible ? versions : compatibleVersions;
    const q = searchTerm.trim().toLowerCase();
    if (!q) return base;
    return base.filter(
      (v) =>
        v.name.toLowerCase().includes(q) ||
        v.version_number.toLowerCase().includes(q) ||
        v.game_versions.some((g) => g.toLowerCase().includes(q)),
    );
  }, [compatibleVersions, searchTerm, showIncompatible, versions]);

  useEffect(() => {
    if (!open || versions.length === 0) return;
    if (selectedId && visibleVersions.some((v) => v.id === selectedId)) return;
    const current =
      visibleVersions.find((v) => v.id === mod?.version_id) ?? visibleVersions[0];
    setSelectedId(current?.id ?? "");
  }, [open, mod?.version_id, selectedId, versions.length, visibleVersions]);

  const selected =
    versions.find((v) => v.id === selectedId) ?? visibleVersions[0] ?? null;
  const { data: selectedDetail, isFetching: loadingSelectedDetail } = useQuery({
    queryKey: ["mod-switch-version-detail", serverId, source, projectId, selected?.id],
    queryFn: () => api.mods.getVersion(serverId, projectId, selected!.id, source),
    enabled: open && !!projectId && !!source && !!selected?.id,
    staleTime: 10 * 60_000,
  });
  const selectedVersion = selectedDetail ?? selected;
  const currentVersion =
    versions.find(
      (v) =>
        (mod?.version_id && v.id === mod.version_id) ||
        v.version_number === mod?.version,
    ) ?? null;
  const selectedIsCurrent =
    !!selected &&
    ((mod?.version_id && selected.id === mod.version_id) ||
      selected.version_number === mod?.version);
  const selectedIsCompatible = selected
    ? versionCompatible(selected, loader, mcVersion)
    : true;
  const actionLabel = switchActionLabel(selectedVersion, currentVersion, versions);

  const switchMutation = useMutation({
    mutationFn: (versionId: string) => api.mods.update(serverId, mod!.id, versionId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] });
      success(`Switched ${mod?.name ?? "mod"} to ${selected?.version_number}`);
      onClose();
    },
    onError: (e: Error) => error("Switch failed", e.message),
  });

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Switch version"
      className="!max-w-6xl !p-0 overflow-hidden"
      headerClassName="mb-0 border-b border-border px-6 py-5 sm:px-8 sm:py-7"
      titleClassName="text-xl sm:text-2xl"
      closeClassName="h-11 w-11 rounded-full bg-surface-2 text-text-secondary hover:bg-surface-2/80 hover:text-text-primary"
      titleIcon={
        project?.icon_url ? (
          <img
            src={project.icon_url}
            alt=""
            className="h-14 w-14 flex-shrink-0 rounded-lg object-cover shadow-lg"
          />
        ) : (
          <span className="flex h-14 w-14 flex-shrink-0 items-center justify-center rounded-lg bg-accent/70 text-black shadow-lg">
            <Package className="h-7 w-7" />
          </span>
        )
      }
    >
      <div className="grid h-[72vh] min-h-[30rem] grid-cols-[21rem_minmax(0,1fr)]">
        <aside className="flex min-h-0 flex-col border-r border-border bg-surface-2/50 p-5">
          <div className="relative mb-3">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-secondary" />
            <Input
              className="pl-9"
              placeholder="Search version..."
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
            />
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto pr-1">
            {isFetching ? (
              <div className="flex justify-center py-10">
                <Loader2 className="h-5 w-5 animate-spin text-accent" />
              </div>
            ) : visibleVersions.length === 0 ? (
              <p className="px-2 py-8 text-center text-sm text-text-secondary">
                No versions found.
              </p>
            ) : (
              <div className="space-y-1">
                {visibleVersions.map((version) => {
                  const current =
                    (mod?.version_id && version.id === mod.version_id) ||
                    version.version_number === mod?.version;
                  const compatibleVersion = versionCompatible(
                    version,
                    loader,
                    mcVersion,
                  );
                  return (
                    <button
                      key={version.id}
                      type="button"
                      onClick={() => setSelectedId(version.id)}
                      className={`flex w-full items-center gap-3 rounded-md px-3 py-2 text-left transition-colors ${
                        selected?.id === version.id
                          ? "bg-accent/25 text-text-primary"
                          : "hover:bg-surface"
                      }`}
                    >
                      <span
                        className={`flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-full text-xs font-semibold ${
                          compatibleVersion
                            ? "bg-green-500/20 text-green-400"
                            : "bg-amber-500/15 text-amber-400"
                        }`}
                      >
                        {(version.version_type ?? "v").charAt(0).toUpperCase()}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-sm font-medium text-text-primary">
                          {version.version_number}
                        </span>
                        <span className="block truncate text-xs text-text-secondary">
                          {version.name || version.game_versions.join(", ")}
                        </span>
                      </span>
                      {current && (
                        <span className="rounded-full bg-surface px-2 py-0.5 text-xs text-text-secondary">
                          Current
                        </span>
                      )}
                    </button>
                  );
                })}
              </div>
            )}
          </div>

          <label className="mt-3 flex w-fit cursor-pointer select-none items-center gap-2 text-sm text-text-secondary">
            <input
              type="checkbox"
              className="accent-accent"
              checked={showIncompatible}
              onChange={(e) => setShowIncompatible(e.target.checked)}
            />
            Show incompatible
          </label>
        </aside>

        <section className="flex min-h-0 flex-col bg-surface">
          {selected ? (
            <>
              <div className="flex items-start justify-between gap-4 border-b border-border px-6 py-5">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <h3 className="truncate text-lg font-semibold text-text-primary">
                      {selectedVersion.version_number}
                    </h3>
                    {selectedVersion.version_type && (
                      <span
                        className={`rounded-full border px-2 py-0.5 text-xs capitalize ${versionTypeClass(
                          selectedVersion.version_type,
                        )}`}
                      >
                        {selectedVersion.version_type}
                      </span>
                    )}
                    {selectedIsCurrent && (
                      <span className="rounded-full border border-border bg-surface-2 px-2 py-0.5 text-xs text-text-secondary">
                        Current
                      </span>
                    )}
                  </div>
                  <p className="mt-1 flex flex-wrap items-center gap-2 text-sm text-text-secondary">
                    <FileText className="h-4 w-4" />
                    Changelog
                    {selectedVersion.loaders.length > 0 && (
                      <>
                        <span>•</span>
                        <span>{selectedVersion.loaders.join(", ")}</span>
                      </>
                    )}
                    {selectedVersion.game_versions.length > 0 && (
                      <>
                        <span>•</span>
                        <span>{selectedVersion.game_versions.join(", ")}</span>
                      </>
                    )}
                  </p>
                </div>
                {selectedVersion.date_published && (
                  <span className="flex-shrink-0 text-sm text-text-secondary">
                    {formatVersionDate(selectedVersion.date_published)}
                  </span>
                )}
              </div>

              <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">
                {loadingSelectedDetail ? (
                  <div className="flex justify-center py-10">
                    <Loader2 className="h-5 w-5 animate-spin text-accent" />
                  </div>
                ) : selectedVersion.changelog ? (
                  <ReactMarkdown
                    remarkPlugins={[remarkGfm]}
                    components={changelogComponents}
                  >
                    {selectedVersion.changelog}
                  </ReactMarkdown>
                ) : (
                  <p className="text-sm text-text-secondary">
                    No changelog provided for this version.
                  </p>
                )}
              </div>
            </>
          ) : (
            <div className="flex flex-1 items-center justify-center text-sm text-text-secondary">
              Select a version
            </div>
          )}

          <div className="flex items-center gap-3 border-t border-border bg-surface-2/40 px-6 py-4">
            <div className="flex min-w-0 flex-1 items-center gap-2 text-sm text-amber-400">
              <AlertTriangle className="h-5 w-5 flex-shrink-0" />
              <span className="truncate">
                Updating can break your instance. Review version changelogs and back up first.
              </span>
            </div>
            <Button variant="outline" onClick={onClose} disabled={switchMutation.isPending}>
              <X className="h-4 w-4" />
              Cancel
            </Button>
            <Button
              onClick={() => selected && switchMutation.mutate(selected.id)}
              loading={switchMutation.isPending}
              disabled={!selected || selectedIsCurrent}
              title={
                selectedIsCompatible
                  ? "Switch version"
                  : "Switch to an incompatible version"
              }
            >
              <Download className="h-4 w-4" />
              {actionLabel}
            </Button>
          </div>
        </section>
      </div>
    </Dialog>
  );
}

function InstalledModRow({
  mod,
  serverId,
  update,
  onUninstall,
  onShowDetails,
  onSwitchVersion,
}: {
  mod: InstalledMod;
  serverId: string;
  update?: ModUpdate;
  onUninstall: () => void;
  onShowDetails?: () => void;
  onSwitchVersion?: () => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();

  const projectQuery = useQuery({
    queryKey: ["mod-project", mod.source, mod.source_id],
    queryFn: () =>
      api.mods.getProject(mod.server_id, mod.source_id!, mod.source),
    enabled: !!mod.source_id,
    staleTime: 10 * 60_000,
  });
  const project = projectQuery.data;

  const updateMutation = useMutation({
    mutationFn: () => api.mods.update(serverId, mod.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] });
      success(`Updated ${mod.name}`);
    },
    onError: (e: Error) => error("Update failed", e.message),
  });

  const pinMutation = useMutation({
    mutationFn: () => api.mods.pin(serverId, mod.id, !mod.pinned),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] });
    },
    onError: (e: Error) => error("Pin failed", e.message),
  });

  const enabledMutation = useMutation({
    mutationFn: () => api.mods.setEnabled(serverId, mod.id, !mod.enabled),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      success(mod.enabled ? `Disabled ${mod.name}` : `Enabled ${mod.name}`);
    },
    onError: (e: Error) => error("Toggle failed", e.message),
  });

  return (
    <div
      className={`grid grid-cols-[1fr_auto] md:grid-cols-[minmax(0,1fr)_minmax(0,18rem)_auto] items-center gap-4 px-4 py-2.5 hover:bg-surface-2/40 border-b border-border/50 ${
        mod.enabled ? "" : "opacity-60"
      }`}
    >
      {/* Project */}
      <div className="flex items-center gap-3 min-w-0">
        {project?.icon_url ? (
          <img
            src={project.icon_url}
            alt=""
            className="h-10 w-10 rounded-md flex-shrink-0 object-cover"
          />
        ) : (
          <div className="h-10 w-10 rounded-md bg-surface-2 flex items-center justify-center flex-shrink-0">
            <Package className="h-4 w-4 text-text-secondary" />
          </div>
        )}
        <div className="min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            {onShowDetails && mod.source_id ? (
              <button
                onClick={onShowDetails}
                className="text-sm font-medium text-text-primary truncate hover:text-accent transition-colors text-left"
                title="View details"
              >
                {project?.title ?? mod.name}
              </button>
            ) : (
              <p className="text-sm font-medium text-text-primary truncate">
                {project?.title ?? mod.name}
              </p>
            )}
            {!mod.enabled && (
              <span className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-surface-2 text-text-secondary border border-border/50">
                disabled
              </span>
            )}
            {mod.installed_as_dep && (
              <span
                className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-surface-2 text-text-secondary border border-border/50"
                title={
                  mod.required_by.length > 0
                    ? `Required by ${mod.required_by.join(", ")}`
                    : "Installed automatically as a dependency"
                }
              >
                dependency
              </span>
            )}
            {mod.orphaned && (
              <span
                className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-red-500/15 text-red-400 border border-red-500/30"
                title="Auto-installed as a dependency, but nothing requires it anymore — safe to remove."
              >
                <Unlink className="h-3 w-3" />
                not needed
              </span>
            )}
            {mod.pinned && (
              <span className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-400 border border-amber-500/30">
                pinned
              </span>
            )}
          </div>
          {mod.required_by.length > 0 && (
            <p className="text-xs text-text-secondary mt-0.5 truncate">
              Required by {mod.required_by.join(", ")}
            </p>
          )}
        </div>
      </div>

      {/* Version (hidden on small screens) */}
      <div className="hidden md:block min-w-0">
        <p className="text-sm text-text-primary truncate">{mod.version}</p>
        <p className="text-xs text-text-secondary truncate">
          {mod.install_path}/{mod.file_name}
        </p>
        {update && (
          <p className="text-xs text-green-400 truncate">
            ↑ {update.latest_version}
          </p>
        )}
      </div>

      {/* Actions */}
      <div className="flex items-center gap-1 flex-shrink-0">
        {onSwitchVersion && (
          <Button
            size="sm"
            variant="ghost"
            onClick={onSwitchVersion}
            title="Switch version"
          >
            <ArrowRightLeft className="h-3.5 w-3.5 text-text-secondary" />
          </Button>
        )}
        {update && (
          <Button
            size="sm"
            variant="outline"
            onClick={() => updateMutation.mutate()}
            loading={updateMutation.isPending}
            title={`Update to ${update.latest_version}`}
          >
            <ArrowUpCircle className="h-3.5 w-3.5" />
          </Button>
        )}
        <Switch
          checked={mod.enabled}
          onChange={() => enabledMutation.mutate()}
          disabled={enabledMutation.isPending}
          title={mod.enabled ? "Disable (keep file)" : "Enable"}
        />
        <Button
          size="sm"
          variant="ghost"
          onClick={() => pinMutation.mutate()}
          loading={pinMutation.isPending}
          title={mod.pinned ? "Unpin (allow updates)" : "Pin (skip updates)"}
        >
          {mod.pinned ? (
            <PinOff className="h-3.5 w-3.5 text-amber-400" />
          ) : (
            <Pin className="h-3.5 w-3.5 text-text-secondary" />
          )}
        </Button>
        <Button size="sm" variant="ghost" onClick={onUninstall} title="Uninstall">
          <Trash2 className="h-3.5 w-3.5 text-red-400" />
        </Button>
      </div>
    </div>
  );
}

function SearchHitCard({
  hit,
  serverId,
  source,
  isModpack,
  projectType,
  loader,
  mcVersion,
  serverLoader,
  serverMc,
  installedIds,
  onShowDetails,
}: {
  hit: ModSearchHit;
  serverId: string;
  source: ModSource;
  isModpack: boolean;
  projectType: ModProjectType;
  /** Browse-filter loader/mc — drives the version picker dropdown. */
  loader: string;
  mcVersion: string;
  /** Server's real loader/mc — drives the install compatibility gate. */
  serverLoader: string;
  serverMc: string;
  installedIds: Set<string>;
  onShowDetails: () => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [picking, setPicking] = useState(false);
  const [selectedVersionId, setSelectedVersionId] = useState<string>("");
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [forcing, setForcing] = useState(false);

  const versionsQuery = useQuery({
    queryKey: ["mod-versions", source, serverId, hit.project_id, loader, mcVersion],
    queryFn: () =>
      api.mods.getVersions(serverId, hit.project_id, loader, mcVersion, source),
    enabled: picking,
  });

  const installMutation = useMutation({
    mutationFn: (versionId: string) =>
      isModpack
        ? api.mods.installModpack(serverId, hit.project_id, versionId)
        : api.mods.install(serverId, source, hit.project_id, versionId, true),
    onSuccess: (created: unknown) => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      setConfirmOpen(false);
      // installRecursive returns [mainMod, ...dependencies]; name the deps so
      // the user can see exactly what got pulled in alongside their pick.
      const list = Array.isArray(created) ? (created as InstalledMod[]) : [];
      const deps = list.slice(1).map((m) => m.name);
      success(
        `Installed ${hit.title}`,
        deps.length > 0
          ? `Pulled ${deps.length} dependenc${deps.length === 1 ? "y" : "ies"}: ${deps.join(", ")}`
          : "No required dependencies",
      );
    },
    onError: (e: Error) => error("Install failed", e.message),
  });

  const isInstalled = installedIds.has(hit.project_id);
  const isCompatible = compatible(
    hit,
    source === "modrinth" ? serverLoader : "",
    serverMc,
    projectType,
  );

  const handleQuickInstall = () => {
    if (isInstalled) return;
    if (!isCompatible) {
      setConfirmOpen(true);
      return;
    }
    // Empty versionId → backend resolves latest compatible.
    installMutation.mutate("");
  };

  // Force-install ignores the server's loader/MC: fetch the newest file with no
  // version filters and install it by explicit id (skips compat resolution).
  const handleForceInstall = async () => {
    setForcing(true);
    try {
      const versions = await api.mods.getVersions(
        serverId,
        hit.project_id,
        "",
        "",
        source,
      );
      if (versions.length === 0) {
        throw new Error("no downloadable versions for this project");
      }
      installMutation.mutate(versions[0].id);
    } catch (e) {
      error("Install failed", (e as Error).message);
    } finally {
      setForcing(false);
    }
  };

  return (
    <div className="rounded-lg border border-border bg-surface hover:border-border/80 transition-colors p-3">
      <div className="flex gap-3">
        <button
          onClick={onShowDetails}
          className="flex-shrink-0"
          title="View details"
        >
          {hit.icon_url ? (
            <img
              src={hit.icon_url}
              alt=""
              className="h-14 w-14 rounded-md object-cover"
            />
          ) : (
            <div className="h-14 w-14 rounded-md bg-surface-2 flex items-center justify-center">
              <Package className="h-6 w-6 text-text-secondary" />
            </div>
          )}
        </button>

        <div className="flex-1 min-w-0">
          <button
            onClick={onShowDetails}
            className="text-left group block max-w-full"
            title="View details"
          >
            <span className="text-sm font-semibold text-text-primary group-hover:text-accent transition-colors">
              {hit.title}
            </span>
            {hit.author && (
              <span className="text-xs text-text-secondary"> by {hit.author}</span>
            )}
          </button>
          <p className="text-xs text-text-secondary line-clamp-2 mt-0.5">
            {hit.description}
          </p>
          {hit.categories.length > 0 && (
            <div className="flex flex-wrap gap-1 mt-1.5">
              {hit.categories.slice(0, 5).map((c) => (
                <span
                  key={c}
                  className="text-[10px] px-1.5 py-0.5 rounded bg-surface-2 text-text-secondary border border-border/50"
                >
                  {c}
                </span>
              ))}
            </div>
          )}
        </div>

        {/* Metrics + action */}
        <div className="flex flex-col items-end justify-between flex-shrink-0 gap-2">
          <div className="text-right text-xs text-text-secondary space-y-0.5">
            <div className="flex items-center justify-end gap-1">
              <Download className="h-3 w-3" />
              {formatDownloads(hit.downloads)}
            </div>
          </div>
          <div className="flex items-center gap-1">
            {!isInstalled && !isCompatible && (
              <span
                className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-400 border border-amber-500/30"
                title="Does not match the server's loader / Minecraft version"
              >
                mismatch
              </span>
            )}
            {isInstalled ? (
              <span className="text-xs text-green-400 px-2 py-1 font-medium">
                Installed
              </span>
            ) : (
              <>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => setPicking((v) => !v)}
                  title="Choose version"
                >
                  {picking ? "Hide" : "Versions"}
                </Button>
                <Button
                  size="sm"
                  variant="default"
                  onClick={handleQuickInstall}
                  loading={installMutation.isPending && !confirmOpen}
                >
                  <Download className="h-3.5 w-3.5" />
                  Install
                </Button>
              </>
            )}
          </div>
        </div>
      </div>

      {picking && !isInstalled && (
        <div className="mt-3 pl-[4.25rem] flex items-center gap-2">
          {versionsQuery.isFetching ? (
            <Loader2 className="h-4 w-4 animate-spin text-accent" />
          ) : versionsQuery.data && versionsQuery.data.length > 0 ? (
            <>
              <select
                className="flex-1 text-xs rounded bg-surface-2 border border-border px-2 py-1 text-text-primary"
                value={selectedVersionId || versionsQuery.data[0].id}
                onChange={(e) => setSelectedVersionId(e.target.value)}
              >
                {versionsQuery.data.map((v) => (
                  <option key={v.id} value={v.id}>
                    {v.version_number} · {v.game_versions.join(", ")}
                  </option>
                ))}
              </select>
              <Button
                size="sm"
                onClick={() =>
                  installMutation.mutate(
                    selectedVersionId || versionsQuery.data![0].id,
                  )
                }
                loading={installMutation.isPending}
              >
                Install
              </Button>
            </>
          ) : (
            <p className="text-xs text-text-secondary">
              No compatible versions for {loader || "any loader"}{" "}
              {mcVersion || "any version"}
            </p>
          )}
        </div>
      )}

      <ConfirmDialog
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        onConfirm={handleForceInstall}
        title="Install anyway?"
        description={`"${hit.title}" doesn't list compatibility with your server (${serverLoader || "any loader"} · ${serverMc || "any version"}). Installing the newest available file may not load correctly.`}
        confirmLabel="Install anyway"
        variant="destructive"
        loading={forcing || installMutation.isPending}
      />
    </div>
  );
}

// Small labelled select used throughout the browse filter sidebar.
function FilterSelect({
  label,
  value,
  onChange,
  children,
  title,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  children: React.ReactNode;
  title?: string;
}) {
  return (
    <div className="space-y-1">
      <label className="text-[10px] uppercase tracking-wide text-text-secondary">
        {label}
      </label>
      <select
        className="w-full text-xs rounded bg-surface-2 border border-border px-2 py-1.5 text-text-primary"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        title={title}
      >
        {children}
      </select>
    </div>
  );
}

export function ModSearch({
  serverId,
  loader,
  mcVersion,
  platform,
}: ModSearchProps) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [query, setQuery] = useState("");
  const [searchInput, setSearchInput] = useState("");
  const [source, setSource] = useState<ModSource>("modrinth");
  const [projectType, setProjectType] = useState<ModProjectType>("mod");
  const [sortIndex, setSortIndex] = useState<ModSortIndex>("relevance");
  const [selectedCats, setSelectedCats] = useState<string[]>([]);
  const [mcFilter, setMcFilter] = useState<string>(mcVersion);
  const [loaderFilter, setLoaderFilter] = useState<string>(loader);
  // Default to server-only content (server-side mods the client doesn't need).
  const [environment, setEnvironment] = useState<string>("server_only");
  const [offset, setOffset] = useState(0);
  const [hideInstalled, setHideInstalled] = useState(false);
  // Name filter for the Installed tab (independent of the Browse search box).
  const [installedFilter, setInstalledFilter] = useState("");
  // Installed status chip: all / updates / enabled / disabled.
  const [statusFilter, setStatusFilter] = useState<
    "all" | "updates" | "enabled" | "disabled"
  >("all");
  const [installedSort, setInstalledSort] = useState<"none" | "alphabetical">(
    "alphabetical",
  );

  const { data: sources } = useQuery({
    queryKey: ["mod-sources", serverId],
    queryFn: () => api.mods.sources(serverId),
    staleTime: 60 * 60_000,
  });
  const curseforgeEnabled = sources?.curseforge ?? false;
  const [uninstallTarget, setUninstallTarget] = useState<InstalledMod | null>(
    null,
  );
  const [switchTarget, setSwitchTarget] = useState<InstalledMod | null>(null);
  const [detailTarget, setDetailTarget] = useState<{
    source: ModSource;
    projectId: string;
    slug?: string;
    isModpack: boolean;
    installed: boolean;
    hit?: ModSearchHit;
  } | null>(null);
  const [detailConfirm, setDetailConfirm] = useState(false);
  const [detailForcing, setDetailForcing] = useState(false);
  const [activeTab, setActiveTab] = useState<"installed" | "search">(
    "installed",
  );
  const [bulkUpdateProgress, setBulkUpdateProgress] =
    useState<BulkUpdateProgress | null>(null);

  // Mods filter by modloader; plugins/datapacks/etc. don't.
  const browseLoader = projectType === "mod" ? loaderFilter : "";

  // Reset paging whenever any browse dimension changes.
  useEffect(() => {
    setOffset(0);
  }, [
    query,
    selectedCats,
    sortIndex,
    projectType,
    mcFilter,
    loaderFilter,
    environment,
    source,
  ]);

  const {
    data: installed = [],
    isLoading: loadingInstalled,
    isFetching: refreshingInstalled,
  } = useQuery({
    queryKey: ["mods", serverId],
    queryFn: () => api.mods.list(serverId),
  });

  const { data: updates = [], isFetching: refreshingUpdates } = useQuery({
    queryKey: ["mod-updates", serverId],
    queryFn: () => api.mods.updates(serverId),
    staleTime: 5 * 60_000,
  });
  const updatesByMod = new Map(updates.map((u) => [u.mod_id, u]));
  const refreshingContent = refreshingInstalled || refreshingUpdates;

  // CurseForge still needs a query; Modrinth browses with an empty one.
  const browseEnabled = source === "modrinth" || query.length >= 2;

  const { data: searchResult, isFetching: searching } = useQuery({
    queryKey: [
      "mod-search",
      serverId,
      source,
      query,
      browseLoader,
      mcFilter,
      projectType,
      sortIndex,
      selectedCats.join(","),
      environment,
      offset,
    ],
    queryFn: () =>
      api.mods.search(serverId, {
        query,
        source,
        loader: browseLoader,
        mcVersion: mcFilter,
        projectType,
        categories: selectedCats,
        index: sortIndex,
        environment,
        limit: PAGE_SIZE,
        offset,
      }),
    enabled: browseEnabled,
  });

  const { data: categories = [] } = useQuery({
    queryKey: ["mod-categories", projectType],
    queryFn: () => api.mods.categories(serverId, projectType),
    enabled: source === "modrinth",
    staleTime: 60 * 60_000,
  });

  const groupedCats = useMemo(() => {
    const m = new Map<string, typeof categories>();
    for (const c of categories) {
      if (!m.has(c.header)) m.set(c.header, []);
      m.get(c.header)!.push(c);
    }
    return [...m.entries()];
  }, [categories]);

  const { data: mcVersions = [] } = useQuery({
    queryKey: ["mc-versions-mods", platform],
    queryFn: () => api.minecraft.versions(platform),
    staleTime: 60 * 60_000,
  });

  const updateAllMutation = useMutation({
    mutationFn: async () => {
      const pendingUpdates = [...updates];
      setBulkUpdateProgress({
        total: pendingUpdates.length,
        completed: 0,
        currentName: pendingUpdates[0]?.name ?? "content",
      });

      for (let i = 0; i < pendingUpdates.length; i += 1) {
        const u = pendingUpdates[i];
        setBulkUpdateProgress({
          total: pendingUpdates.length,
          completed: i,
          currentName: u.name,
        });
        await api.mods.update(serverId, u.mod_id);
        setBulkUpdateProgress({
          total: pendingUpdates.length,
          completed: i + 1,
          currentName:
            pendingUpdates[i + 1]?.name ?? "finishing content updates",
        });
      }
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] });
      success("All mods updated");
    },
    onError: (e: Error) => error("Update failed", e.message),
    onSettled: () => setBulkUpdateProgress(null),
  });

  const refreshInstalledContent = async () => {
    await Promise.all([
      qc.invalidateQueries({ queryKey: ["mods", serverId] }),
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] }),
    ]);
  };

  const uninstallMutation = useMutation({
    mutationFn: (modId: string) => api.mods.uninstall(serverId, modId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      success("Mod uninstalled");
      setUninstallTarget(null);
    },
    onError: (e: Error) => error("Uninstall failed", e.message),
  });

  // Install from the detail dialog. versionId "" → latest compatible; an explicit
  // id force-installs that exact file (used for the mismatch override).
  const detailInstallMutation = useMutation({
    mutationFn: (versionId: string) => {
      if (!detailTarget) return Promise.resolve();
      return detailTarget.isModpack
        ? api.mods.installModpack(serverId, detailTarget.projectId, versionId)
        : api.mods.install(
            serverId,
            detailTarget.source,
            detailTarget.projectId,
            versionId,
            true,
          );
    },
    onSuccess: (created: unknown) => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      const list = Array.isArray(created) ? (created as InstalledMod[]) : [];
      const deps = list.slice(1).map((m) => m.name);
      success(
        "Mod installed",
        deps.length > 0
          ? `Pulled dependencies: ${deps.join(", ")}`
          : "No required dependencies",
      );
      setDetailTarget(null);
      setDetailConfirm(false);
    },
    onError: (e: Error) => error("Install failed", e.message),
  });

  const handleDetailInstall = () => {
    if (!detailTarget) return;
    const hit = detailTarget.hit;
    // Only the browse-originated detail dialog carries a hit to gate on.
    if (
      hit &&
      !compatible(
        hit,
        detailTarget.source === "modrinth" ? loader : "",
        mcVersion,
        projectType,
      )
    ) {
      setDetailConfirm(true);
      return;
    }
    detailInstallMutation.mutate("");
  };

  const handleDetailForce = async () => {
    if (!detailTarget) return;
    setDetailForcing(true);
    try {
      const versions = await api.mods.getVersions(
        serverId,
        detailTarget.projectId,
        "",
        "",
        detailTarget.source,
      );
      if (versions.length === 0) {
        throw new Error("no downloadable versions for this project");
      }
      detailInstallMutation.mutate(versions[0].id);
    } catch (e) {
      error("Install failed", (e as Error).message);
    } finally {
      setDetailForcing(false);
    }
  };

  const installedProjectIds = new Set(installed.map((m) => m.source_id ?? ""));

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault();
    setQuery(searchInput.trim());
    setActiveTab("search");
  };

  const toggleCat = (name: string) => {
    setSelectedCats((prev) =>
      prev.includes(name) ? prev.filter((c) => c !== name) : [...prev, name],
    );
  };

  const totalHits = searchResult?.total_hits ?? 0;
  const totalPages = Math.max(1, Math.ceil(totalHits / PAGE_SIZE));
  const page = Math.floor(offset / PAGE_SIZE) + 1;

  // Optionally drop already-installed projects from the browse list. Filtering is
  // client-side (per page), so the page/total counts still reflect the full set.
  const visibleHits = (searchResult?.hits ?? []).filter(
    (h) => !hideInstalled || !installedProjectIds.has(h.project_id),
  );

  // Installed tab: status chip + case-insensitive name/file filter.
  const filterText = installedFilter.trim().toLowerCase();
  const enabledCount = installed.filter((m) => m.enabled).length;
  const updatesCount = updates.length;
  const visibleInstalled = installed.filter((m) => {
    if (statusFilter === "enabled" && !m.enabled) return false;
    if (statusFilter === "disabled" && m.enabled) return false;
    if (statusFilter === "updates" && !updatesByMod.has(m.id)) return false;
    if (
      filterText &&
      !m.name.toLowerCase().includes(filterText) &&
      !m.file_name.toLowerCase().includes(filterText)
    )
      return false;
    return true;
  }).sort((a, b) => {
    if (installedSort !== "alphabetical") return 0;
    return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
  });

  // Build the numbered pagination window (max 7 entries, current centred).
  const pageNumbers = useMemo(() => {
    const span = 5;
    let start = Math.max(1, page - Math.floor(span / 2));
    const end = Math.min(totalPages, start + span - 1);
    start = Math.max(1, end - span + 1);
    const nums: number[] = [];
    for (let i = start; i <= end; i++) nums.push(i);
    return nums;
  }, [page, totalPages]);

  const STATUS_CHIPS: { value: typeof statusFilter; label: string }[] = [
    { value: "all", label: `All (${installed.length})` },
    { value: "updates", label: `Updates (${updatesCount})` },
    { value: "enabled", label: `Enabled (${enabledCount})` },
    { value: "disabled", label: `Disabled (${installed.length - enabledCount})` },
  ];
  const bulkUpdatePercent =
    bulkUpdateProgress && bulkUpdateProgress.total > 0
      ? Math.round(
          (bulkUpdateProgress.completed / bulkUpdateProgress.total) * 100,
        )
      : 0;

  return (
    <div className="flex flex-col h-full">
      {/* Tab bar */}
      <div className="flex border-b border-border bg-surface items-center">
        <button
          onClick={() => setActiveTab("installed")}
          className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
            activeTab === "installed"
              ? "text-text-primary border-accent"
              : "text-text-secondary border-transparent hover:text-text-primary"
          }`}
        >
          Installed ({installed.length})
        </button>
        <button
          onClick={() => setActiveTab("search")}
          className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
            activeTab === "search"
              ? "text-text-primary border-accent"
              : "text-text-secondary border-transparent hover:text-text-primary"
          }`}
        >
          Browse
          {searchResult && ` (${searchResult.total_hits})`}
        </button>
        <div className="ml-auto flex items-center gap-2 mr-2">
          {activeTab === "installed" && (
            <Button
              size="sm"
              variant="default"
              onClick={() => setActiveTab("search")}
            >
              <Compass className="h-3.5 w-3.5" />
              Browse content
            </Button>
          )}
        </div>
      </div>

      {activeTab === "installed" ? (
        /* ── Installed: status chips + name filter, then a table ───────── */
        <>
          <div className="px-4 py-3 border-b border-border bg-surface flex flex-wrap items-center gap-2">
            <div className="flex items-center gap-1">
              {STATUS_CHIPS.map((c) => (
                <button
                  key={c.value}
                  onClick={() => setStatusFilter(c.value)}
                  className={`text-xs px-3 py-1 rounded-full border transition-colors ${
                    statusFilter === c.value
                      ? "bg-accent/15 text-accent border-accent/40"
                      : "bg-surface-2 text-text-secondary border-border hover:text-text-primary"
                  }`}
                >
                  {c.label}
                </button>
              ))}
            </div>
            <button
              type="button"
              onClick={() =>
                setInstalledSort((s) =>
                  s === "alphabetical" ? "none" : "alphabetical",
                )
              }
              className={`inline-flex h-7 items-center gap-1.5 rounded-full border px-3 text-xs font-medium transition-colors ${
                installedSort === "alphabetical"
                  ? "bg-surface-2 text-text-primary border-border"
                  : "bg-surface text-text-secondary border-border hover:text-text-primary"
              }`}
              title="Sort installed content alphabetically"
            >
              <ArrowDownAZ className="h-3.5 w-3.5" />
              Alphabetical
            </button>
            <div className="relative flex-1 min-w-[12rem]">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-text-secondary pointer-events-none" />
              <Input
                className="pl-9"
                placeholder="Filter installed content…"
                value={installedFilter}
                onChange={(e) => setInstalledFilter(e.target.value)}
              />
            </div>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => updateAllMutation.mutate()}
              loading={updateAllMutation.isPending}
              disabled={updates.length === 0 || updateAllMutation.isPending}
              title={
                updates.length > 0
                  ? `Update ${updates.length} installed item${updates.length === 1 ? "" : "s"}`
                  : "No updates available"
              }
            >
              <Download className="h-3.5 w-3.5" />
              Update all
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={refreshInstalledContent}
              loading={refreshingContent}
              title="Refresh installed content"
            >
              <RefreshCw className="h-3.5 w-3.5" />
              Refresh
            </Button>
          </div>
          {bulkUpdateProgress && (
            <div className="border-b border-border bg-surface px-4 py-3">
              <div className="mb-2 flex items-center justify-between gap-3 text-xs">
                <span className="min-w-0 truncate text-text-secondary">
                  Updating {bulkUpdateProgress.currentName}
                </span>
                <span className="flex-shrink-0 tabular-nums text-text-primary">
                  {bulkUpdateProgress.completed}/{bulkUpdateProgress.total}
                </span>
              </div>
              <div
                className="h-2 overflow-hidden rounded-full bg-surface-2"
                role="progressbar"
                aria-valuemin={0}
                aria-valuemax={bulkUpdateProgress.total}
                aria-valuenow={bulkUpdateProgress.completed}
                aria-label="Content update progress"
              >
                <div
                  className="h-full rounded-full bg-accent transition-all duration-300"
                  style={{ width: `${bulkUpdatePercent}%` }}
                />
              </div>
            </div>
          )}

          <div className="flex-1 overflow-y-auto">
            {loadingInstalled ? (
              <div className="flex justify-center py-8">
                <Loader2 className="h-5 w-5 animate-spin text-accent" />
              </div>
            ) : installed.length === 0 ? (
              <div className="text-center py-12 text-text-secondary">
                <Package className="h-8 w-8 mx-auto mb-2 opacity-30" />
                <p className="text-sm">No content installed</p>
                <Button
                  size="sm"
                  variant="outline"
                  className="mt-3"
                  onClick={() => setActiveTab("search")}
                >
                  <Compass className="h-3.5 w-3.5" /> Browse content
                </Button>
              </div>
            ) : visibleInstalled.length === 0 ? (
              <div className="text-center py-12 text-text-secondary">
                <p className="text-sm">
                  {statusFilter === "updates"
                    ? "No installed content has updates"
                    : "No installed content matches the filter"}
                </p>
              </div>
            ) : (
              <>
                {/* Column header */}
                <div className="grid grid-cols-[1fr_auto] md:grid-cols-[minmax(0,1fr)_minmax(0,18rem)_auto] gap-4 px-4 py-2 border-b border-border bg-surface-2/30 text-[10px] uppercase tracking-wide text-text-secondary sticky top-0">
                  <span>Project</span>
                  <span className="hidden md:block">Version</span>
                  <span className="text-right">Actions</span>
                </div>
                {visibleInstalled.map((mod) => (
                  <InstalledModRow
                    key={mod.id}
                    mod={mod}
                    serverId={serverId}
                    update={updatesByMod.get(mod.id)}
                    onUninstall={() => setUninstallTarget(mod)}
                    onSwitchVersion={
                      mod.source_id &&
                      (mod.source === "modrinth" || mod.source === "curseforge")
                        ? () => setSwitchTarget(mod)
                        : undefined
                    }
                    onShowDetails={
                      mod.source_id
                        ? () =>
                            setDetailTarget({
                              source: mod.source as ModSource,
                              projectId: mod.source_id!,
                              isModpack: false,
                              installed: true,
                            })
                        : undefined
                    }
                  />
                ))}
              </>
            )}
          </div>
        </>
      ) : (
        /* ── Browse: search bar on top, results + right filter sidebar ─── */
        <div className="flex flex-1 min-h-0">
          {/* Main column */}
          <div className="flex-1 min-w-0 flex flex-col">
            <div className="px-4 py-3 border-b border-border bg-surface space-y-2">
              <form onSubmit={handleSearch} className="flex gap-2">
                <div className="relative flex-1">
                  <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-text-secondary pointer-events-none" />
                  <Input
                    className="pl-9"
                    placeholder={
                      source === "curseforge"
                        ? "Search CurseForge…"
                        : "Search content…"
                    }
                    value={searchInput}
                    onChange={(e) => setSearchInput(e.target.value)}
                  />
                </div>
                <Button type="submit" size="md" loading={searching}>
                  {source === "curseforge" ? "Search" : "Apply"}
                </Button>
              </form>
              <label className="flex items-center gap-2 text-xs text-text-secondary cursor-pointer select-none w-fit">
                <input
                  type="checkbox"
                  className="accent-accent"
                  checked={hideInstalled}
                  onChange={(e) => setHideInstalled(e.target.checked)}
                />
                Hide already installed content
              </label>
            </div>

            <div className="flex-1 overflow-y-auto p-4">
              {!browseEnabled ? (
                <div className="text-center py-12 text-text-secondary">
                  <Search className="h-8 w-8 mx-auto mb-2 opacity-30" />
                  <p className="text-sm">Enter at least 2 characters to search</p>
                </div>
              ) : searching ? (
                <div className="flex justify-center py-8">
                  <Loader2 className="h-5 w-5 animate-spin text-accent" />
                </div>
              ) : !searchResult || visibleHits.length === 0 ? (
                <div className="text-center py-12 text-text-secondary">
                  <p className="text-sm">
                    {hideInstalled && (searchResult?.hits.length ?? 0) > 0
                      ? "All results on this page are already installed"
                      : query
                        ? `No results for "${query}"`
                        : "No results"}
                  </p>
                </div>
              ) : (
                <>
                  <div className="space-y-2">
                    {visibleHits.map((hit) => (
                      <SearchHitCard
                        key={hit.project_id}
                        hit={hit}
                        serverId={serverId}
                        source={source}
                        isModpack={
                          projectType === "modpack" && source === "modrinth"
                        }
                        projectType={projectType}
                        loader={browseLoader}
                        mcVersion={mcFilter}
                        serverLoader={loader}
                        serverMc={mcVersion}
                        installedIds={installedProjectIds}
                        onShowDetails={() =>
                          setDetailTarget({
                            source,
                            projectId: hit.project_id,
                            slug: hit.slug,
                            isModpack:
                              projectType === "modpack" && source === "modrinth",
                            installed: installedProjectIds.has(hit.project_id),
                            hit,
                          })
                        }
                      />
                    ))}
                  </div>

                  {/* Numbered pagination */}
                  {totalPages > 1 && (
                    <div className="flex items-center justify-center gap-1 pt-4">
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={offset === 0}
                        onClick={() =>
                          setOffset((o) => Math.max(0, o - PAGE_SIZE))
                        }
                      >
                        Prev
                      </Button>
                      {pageNumbers[0] > 1 && (
                        <span className="px-1 text-xs text-text-secondary">…</span>
                      )}
                      {pageNumbers.map((n) => (
                        <button
                          key={n}
                          onClick={() => setOffset((n - 1) * PAGE_SIZE)}
                          className={`h-7 min-w-7 px-2 rounded text-xs transition-colors ${
                            n === page
                              ? "bg-accent text-black font-medium"
                              : "text-text-secondary hover:bg-surface-2"
                          }`}
                        >
                          {n}
                        </button>
                      ))}
                      {pageNumbers[pageNumbers.length - 1] < totalPages && (
                        <span className="px-1 text-xs text-text-secondary">…</span>
                      )}
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={page >= totalPages}
                        onClick={() => setOffset((o) => o + PAGE_SIZE)}
                      >
                        Next
                      </Button>
                    </div>
                  )}
                </>
              )}
            </div>
          </div>

          {/* Right filter sidebar */}
          <aside className="w-60 flex-shrink-0 border-l border-border bg-surface overflow-y-auto p-4 space-y-4">
            <FilterSelect
              label="Source"
              value={source}
              onChange={(v) => setSource(v as ModSource)}
              title="Mod source"
            >
              <option value="modrinth">Modrinth</option>
              <option value="curseforge" disabled={!curseforgeEnabled}>
                CurseForge{curseforgeEnabled ? "" : " (no API key)"}
              </option>
            </FilterSelect>

            <FilterSelect
              label="Content type"
              value={projectType}
              onChange={(v) => {
                setProjectType(v as ModProjectType);
                setSelectedCats([]);
              }}
            >
              {PROJECT_TYPES.map((t) => (
                <option key={t.value} value={t.value}>
                  {t.label}
                </option>
              ))}
            </FilterSelect>

            <FilterSelect
              label="Sort by"
              value={sortIndex}
              onChange={(v) => setSortIndex(v as ModSortIndex)}
            >
              {SORTS.map((s) => (
                <option key={s.value} value={s.value}>
                  {s.label}
                </option>
              ))}
            </FilterSelect>

            <FilterSelect
              label="Game version"
              value={mcFilter}
              onChange={setMcFilter}
              title="Minecraft version"
            >
              <option value="">Any version</option>
              {mcVersions.map((v) => (
                <option key={v.version} value={v.version}>
                  {v.version}
                </option>
              ))}
            </FilterSelect>

            {projectType === "mod" && (
              <FilterSelect
                label="Loader"
                value={loaderFilter}
                onChange={setLoaderFilter}
                title="Loader"
              >
                <option value="">Any loader</option>
                {LOADERS.map((l) => (
                  <option key={l} value={l}>
                    {l}
                  </option>
                ))}
              </FilterSelect>
            )}

            <FilterSelect
              label="Environment"
              value={environment}
              onChange={setEnvironment}
              title="Environment"
            >
              <option value="">Server (any)</option>
              <option value="server_only">Server only</option>
              <option value="client_server">Client + Server</option>
              <option value="client">Client</option>
              <option value="any">Any</option>
            </FilterSelect>

            {/* Category tags (Modrinth only) */}
            {source === "modrinth" && groupedCats.length > 0 && (
              <div className="space-y-3 pt-1">
                <div className="flex items-center justify-between">
                  <span className="text-[10px] uppercase tracking-wide text-text-secondary">
                    Categories
                  </span>
                  {selectedCats.length > 0 && (
                    <button
                      onClick={() => setSelectedCats([])}
                      className="text-[10px] text-accent hover:underline"
                    >
                      Clear ({selectedCats.length})
                    </button>
                  )}
                </div>
                {groupedCats.map(([header, cats]) => (
                  <div key={header} className="space-y-1.5">
                    <p className="text-[10px] uppercase tracking-wide text-text-secondary/70">
                      {header}
                    </p>
                    <div className="flex flex-wrap gap-1">
                      {cats.map((c) => {
                        const active = selectedCats.includes(c.name);
                        return (
                          <button
                            key={c.name}
                            onClick={() => toggleCat(c.name)}
                            className={`inline-flex items-center gap-1 text-xs px-2 py-1 rounded border transition-colors ${
                              active
                                ? "bg-accent/15 text-accent border-accent/40"
                                : "bg-surface-2 text-text-secondary border-border hover:text-text-primary"
                            }`}
                          >
                            <span
                              className="h-3.5 w-3.5 inline-flex items-center justify-center [&_svg]:h-full [&_svg]:w-full"
                              dangerouslySetInnerHTML={{ __html: c.icon }}
                            />
                            {c.name}
                          </button>
                        );
                      })}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </aside>
        </div>
      )}

      <ConfirmDialog
        open={uninstallTarget !== null}
        onClose={() => setUninstallTarget(null)}
        onConfirm={() =>
          uninstallTarget && uninstallMutation.mutate(uninstallTarget.id)
        }
        title="Uninstall mod"
        description={`Uninstall "${uninstallTarget?.name}"? The file will be removed from the server.`}
        confirmLabel="Uninstall"
        variant="destructive"
        loading={uninstallMutation.isPending}
      />

      <VersionSwitchDialog
        open={switchTarget !== null}
        onClose={() => setSwitchTarget(null)}
        serverId={serverId}
        mod={switchTarget}
        loader={loader}
        mcVersion={mcVersion}
      />

      <ConfirmDialog
        open={detailConfirm}
        onClose={() => setDetailConfirm(false)}
        onConfirm={handleDetailForce}
        title="Install anyway?"
        description={`This project doesn't list compatibility with your server (${loader || "any loader"} · ${mcVersion || "any version"}). Installing the newest available file may not load correctly.`}
        confirmLabel="Install anyway"
        variant="destructive"
        loading={detailForcing || detailInstallMutation.isPending}
      />

      {detailTarget && (
        <ModDetailDialog
          open={detailTarget !== null}
          onClose={() => setDetailTarget(null)}
          serverId={serverId}
          source={detailTarget.source}
          projectId={detailTarget.projectId}
          slug={detailTarget.slug}
          installed={detailTarget.installed}
          installing={detailInstallMutation.isPending}
          onInstall={detailTarget.installed ? undefined : handleDetailInstall}
        />
      )}
    </div>
  );
}
