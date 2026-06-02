import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Search,
  Package,
  Trash2,
  Download,
  Loader2,
  Pin,
  PinOff,
  ArrowUpCircle,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/ui/dialog";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type {
  InstalledMod,
  ModProjectType,
  ModSearchHit,
  ModSortIndex,
  ModSource,
  ModUpdate,
} from "@/lib/types";

interface ModSearchProps {
  serverId: string;
  loader: string;
  mcVersion: string;
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

function formatDownloads(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(0)}K`;
  return String(n);
}

function InstalledModRow({
  mod,
  serverId,
  update,
  onUninstall,
}: {
  mod: InstalledMod;
  serverId: string;
  update?: ModUpdate;
  onUninstall: () => void;
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

  return (
    <div className="flex items-start justify-between gap-3 px-4 py-3 hover:bg-surface-2/50 border-b border-border/50">
      <div className="flex items-start gap-3 min-w-0">
        {project?.icon_url ? (
          <img
            src={project.icon_url}
            alt=""
            className="h-9 w-9 rounded flex-shrink-0 object-cover"
          />
        ) : (
          <div className="h-9 w-9 rounded bg-surface-2 flex items-center justify-center flex-shrink-0">
            <Package className="h-4 w-4 text-text-secondary" />
          </div>
        )}
        <div className="min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <p className="text-sm font-medium text-text-primary truncate">
              {project?.title ?? mod.name}
            </p>
            {mod.installed_as_dep && (
              <span className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-surface-2 text-text-secondary border border-border/50">
                dependency
              </span>
            )}
            {mod.pinned && (
              <span className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-400 border border-amber-500/30">
                pinned
              </span>
            )}
          </div>
          <p className="text-xs text-text-secondary mt-0.5">
            {mod.version} · {mod.install_path}/{mod.file_name}
          </p>
          {update && (
            <p className="text-xs text-green-400 mt-0.5">
              Update available: {update.latest_version}
            </p>
          )}
        </div>
      </div>
      <div className="flex items-center gap-1 flex-shrink-0">
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

function SearchHitRow({
  hit,
  serverId,
  source,
  isModpack,
  loader,
  mcVersion,
  installedIds,
}: {
  hit: ModSearchHit;
  serverId: string;
  source: ModSource;
  isModpack: boolean;
  loader: string;
  mcVersion: string;
  installedIds: Set<string>;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [picking, setPicking] = useState(false);
  const [selectedVersionId, setSelectedVersionId] = useState<string>("");

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
      const n = Array.isArray(created) ? created.length : 1;
      success(
        `Installed ${hit.title}`,
        n > 1 ? `+ ${n - 1} dependencies` : undefined,
      );
    },
    onError: (e: Error) => error("Install failed", e.message),
  });

  const isInstalled = installedIds.has(hit.project_id);

  const handleQuickInstall = async () => {
    if (isInstalled) return;
    // Empty versionId → backend resolves latest compatible.
    installMutation.mutate("");
  };

  return (
    <div className="px-4 py-3 hover:bg-surface-2/50 border-b border-border/50">
      <div className="flex items-start gap-3">
        {hit.icon_url ? (
          <img
            src={hit.icon_url}
            alt=""
            className="h-9 w-9 rounded flex-shrink-0 object-cover"
          />
        ) : (
          <div className="h-9 w-9 rounded bg-surface-2 flex items-center justify-center flex-shrink-0">
            <Package className="h-4 w-4 text-text-secondary" />
          </div>
        )}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <p className="text-sm font-medium text-text-primary truncate">
              {hit.title}
            </p>
            <span className="text-xs text-text-secondary flex-shrink-0">
              {formatDownloads(hit.downloads)} ↓
            </span>
          </div>
          <p className="text-xs text-text-secondary line-clamp-1 mt-0.5">
            {hit.description}
          </p>
        </div>
        <div className="flex-shrink-0 flex items-center gap-1">
          {isInstalled ? (
            <span className="text-xs text-green-400 px-2 py-1">Installed</span>
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
                variant="outline"
                onClick={handleQuickInstall}
                loading={installMutation.isPending}
              >
                <Download className="h-3.5 w-3.5" />
                Install
              </Button>
            </>
          )}
        </div>
      </div>

      {picking && !isInstalled && (
        <div className="mt-2 pl-12 flex items-center gap-2">
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
              No compatible versions for {loader} {mcVersion}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

export function ModSearch({ serverId, loader, mcVersion }: ModSearchProps) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [query, setQuery] = useState("");
  const [searchInput, setSearchInput] = useState("");
  const [source, setSource] = useState<ModSource>("modrinth");
  const [projectType, setProjectType] = useState<ModProjectType>("mod");
  const [sortIndex, setSortIndex] = useState<ModSortIndex>("relevance");

  const { data: sources } = useQuery({
    queryKey: ["mod-sources", serverId],
    queryFn: () => api.mods.sources(serverId),
    staleTime: 60 * 60_000,
  });
  const curseforgeEnabled = sources?.curseforge ?? false;
  const [uninstallTarget, setUninstallTarget] = useState<InstalledMod | null>(
    null,
  );
  const [activeTab, setActiveTab] = useState<"installed" | "search">(
    "installed",
  );

  // Plugins/datapacks don't filter by modloader; mods do.
  const searchLoader = projectType === "mod" ? loader : "";

  const { data: installed = [], isLoading: loadingInstalled } = useQuery({
    queryKey: ["mods", serverId],
    queryFn: () => api.mods.list(serverId),
  });

  const { data: updates = [] } = useQuery({
    queryKey: ["mod-updates", serverId],
    queryFn: () => api.mods.updates(serverId),
    staleTime: 5 * 60_000,
  });
  const updatesByMod = new Map(updates.map((u) => [u.mod_id, u]));

  const { data: searchResult, isFetching: searching } = useQuery({
    queryKey: [
      "mod-search",
      serverId,
      source,
      query,
      searchLoader,
      mcVersion,
      projectType,
      sortIndex,
    ],
    queryFn: () =>
      api.mods.search(serverId, {
        query,
        source,
        loader: searchLoader,
        mcVersion,
        projectType,
        index: sortIndex,
      }),
    enabled: query.length >= 2,
  });

  const updateAllMutation = useMutation({
    mutationFn: async () => {
      for (const u of updates) {
        await api.mods.update(serverId, u.mod_id);
      }
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] });
      success("All mods updated");
    },
    onError: (e: Error) => error("Update failed", e.message),
  });

  const uninstallMutation = useMutation({
    mutationFn: (modId: string) => api.mods.uninstall(serverId, modId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      success("Mod uninstalled");
      setUninstallTarget(null);
    },
    onError: (e: Error) => error("Uninstall failed", e.message),
  });

  const installedProjectIds = new Set(installed.map((m) => m.source_id ?? ""));

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault();
    setQuery(searchInput.trim());
    if (searchInput.trim().length >= 2) setActiveTab("search");
  };

  return (
    <div className="flex flex-col h-full">
      {/* Search bar + filters */}
      <div className="px-4 py-3 border-b border-border bg-surface space-y-2">
        <form onSubmit={handleSearch} className="flex gap-2">
          <div className="relative flex-1">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-text-secondary pointer-events-none" />
            <Input
              className="pl-9"
              placeholder="Search Modrinth…"
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
            />
          </div>
          <Button type="submit" size="sm" loading={searching}>
            Search
          </Button>
        </form>
        <div className="flex gap-2">
          <select
            className="text-xs rounded bg-surface-2 border border-border px-2 py-1 text-text-primary"
            value={source}
            onChange={(e) => setSource(e.target.value as ModSource)}
            title="Mod source"
          >
            <option value="modrinth">Modrinth</option>
            <option value="curseforge" disabled={!curseforgeEnabled}>
              CurseForge{curseforgeEnabled ? "" : " (no API key)"}
            </option>
          </select>
          <select
            className="text-xs rounded bg-surface-2 border border-border px-2 py-1 text-text-primary"
            value={projectType}
            onChange={(e) =>
              setProjectType(e.target.value as ModProjectType)
            }
          >
            {PROJECT_TYPES.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>
          <select
            className="text-xs rounded bg-surface-2 border border-border px-2 py-1 text-text-primary"
            value={sortIndex}
            onChange={(e) => setSortIndex(e.target.value as ModSortIndex)}
          >
            {SORTS.map((s) => (
              <option key={s.value} value={s.value}>
                Sort: {s.label}
              </option>
            ))}
          </select>
        </div>
      </div>

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
          Search Results
          {searchResult && ` (${searchResult.total_hits})`}
        </button>
        {updates.length > 0 && activeTab === "installed" && (
          <Button
            size="sm"
            variant="outline"
            className="ml-auto mr-2 my-1"
            onClick={() => updateAllMutation.mutate()}
            loading={updateAllMutation.isPending}
          >
            <ArrowUpCircle className="h-3.5 w-3.5" />
            Update all ({updates.length})
          </Button>
        )}
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto">
        {activeTab === "installed" ? (
          loadingInstalled ? (
            <div className="flex justify-center py-8">
              <Loader2 className="h-5 w-5 animate-spin text-accent" />
            </div>
          ) : installed.length === 0 ? (
            <div className="text-center py-12 text-text-secondary">
              <Package className="h-8 w-8 mx-auto mb-2 opacity-30" />
              <p className="text-sm">No mods installed</p>
            </div>
          ) : (
            installed.map((mod) => (
              <InstalledModRow
                key={mod.id}
                mod={mod}
                serverId={serverId}
                update={updatesByMod.get(mod.id)}
                onUninstall={() => setUninstallTarget(mod)}
              />
            ))
          )
        ) : query.length < 2 ? (
          <div className="text-center py-12 text-text-secondary">
            <Search className="h-8 w-8 mx-auto mb-2 opacity-30" />
            <p className="text-sm">Enter at least 2 characters to search</p>
          </div>
        ) : searching ? (
          <div className="flex justify-center py-8">
            <Loader2 className="h-5 w-5 animate-spin text-accent" />
          </div>
        ) : !searchResult || searchResult.hits.length === 0 ? (
          <div className="text-center py-12 text-text-secondary">
            <p className="text-sm">No results for "{query}"</p>
          </div>
        ) : (
          searchResult.hits.map((hit) => (
            <SearchHitRow
              key={hit.project_id}
              hit={hit}
              serverId={serverId}
              source={source}
              isModpack={projectType === "modpack" && source === "modrinth"}
              loader={searchLoader}
              mcVersion={mcVersion}
              installedIds={installedProjectIds}
            />
          ))
        )}
      </div>

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
    </div>
  );
}
