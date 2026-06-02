import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Search, Package, Trash2, Download, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/ui/dialog";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { InstalledMod, ModSearchHit, ModVersion } from "@/lib/types";

interface ModSearchProps {
  serverId: string;
  loader: string;
  mcVersion: string;
}

function formatDownloads(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(0)}K`;
  return String(n);
}

function InstalledModRow({
  mod,
  onUninstall,
}: {
  mod: InstalledMod;
  onUninstall: () => void;
}) {
  const projectQuery = useQuery({
    queryKey: ["modrinth-project", mod.source_id],
    queryFn: () => api.mods.getProject(mod.server_id, mod.source_id!),
    enabled: mod.source === "modrinth" && !!mod.source_id,
    staleTime: 10 * 60_000,
  });

  const project = projectQuery.data;

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
          <div className="flex items-center gap-2">
            <p className="text-sm font-medium text-text-primary truncate">
              {project?.title ?? mod.name}
            </p>
            {project && (
              <span className="text-xs text-text-secondary flex-shrink-0">
                {formatDownloads(project.downloads)} ↓
              </span>
            )}
          </div>
          {project?.description && (
            <p className="text-xs text-text-secondary line-clamp-1 mt-0.5">
              {project.description}
            </p>
          )}
          <p className="text-xs text-text-secondary mt-0.5">
            {mod.version} · {mod.file_name}
          </p>
          {project?.categories && project.categories.length > 0 && (
            <div className="flex flex-wrap gap-1 mt-1">
              {project.categories.slice(0, 4).map((c) => (
                <span
                  key={c}
                  className="text-xs px-1.5 py-0.5 rounded bg-surface-2 text-text-secondary border border-border/50"
                >
                  {c}
                </span>
              ))}
            </div>
          )}
        </div>
      </div>
      <Button size="sm" variant="ghost" onClick={onUninstall} title="Uninstall">
        <Trash2 className="h-3.5 w-3.5 text-red-400" />
      </Button>
    </div>
  );
}

function SearchHitRow({
  hit,
  serverId,
  loader,
  mcVersion,
  installedIds,
}: {
  hit: ModSearchHit;
  serverId: string;
  loader: string;
  mcVersion: string;
  installedIds: Set<string>;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [selectedVersionId, setSelectedVersionId] = useState<string | null>(
    null,
  );

  const versionsQuery = useQuery({
    queryKey: ["mod-versions", serverId, hit.project_id, loader, mcVersion],
    queryFn: () =>
      api.mods.getVersions(serverId, hit.project_id, loader, mcVersion),
    enabled: false,
  });

  const installMutation = useMutation({
    mutationFn: (versionId: string) =>
      api.mods.install(serverId, "modrinth", hit.project_id, versionId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      success(`Installed ${hit.title}`);
    },
    onError: (e: Error) => error("Install failed", e.message),
  });

  const isInstalled = installedIds.has(hit.project_id);

  const handleInstall = async () => {
    if (isInstalled) return;
    let versions = versionsQuery.data;
    if (!versions) {
      const result = await versionsQuery.refetch();
      versions = result.data;
    }
    if (!versions || versions.length === 0) {
      error(
        "No compatible versions",
        `No versions found for ${loader} ${mcVersion}`,
      );
      return;
    }
    const versionId = selectedVersionId ?? versions[0].id;
    installMutation.mutate(versionId);
  };

  return (
    <div className="flex items-start gap-3 px-4 py-3 hover:bg-surface-2/50 border-b border-border/50">
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
        {hit.categories.length > 0 && (
          <div className="flex flex-wrap gap-1 mt-1">
            {hit.categories.slice(0, 4).map((c) => (
              <span
                key={c}
                className="text-xs px-1.5 py-0.5 rounded bg-surface-2 text-text-secondary border border-border/50"
              >
                {c}
              </span>
            ))}
          </div>
        )}
      </div>
      <div className="flex-shrink-0">
        {isInstalled ? (
          <span className="text-xs text-green-400 px-2 py-1">Installed</span>
        ) : (
          <Button
            size="sm"
            variant="outline"
            onClick={handleInstall}
            loading={installMutation.isPending || versionsQuery.isFetching}
          >
            <Download className="h-3.5 w-3.5" />
            Install
          </Button>
        )}
      </div>
    </div>
  );
}

export function ModSearch({ serverId, loader, mcVersion }: ModSearchProps) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [query, setQuery] = useState("");
  const [searchInput, setSearchInput] = useState("");
  const [uninstallTarget, setUninstallTarget] = useState<InstalledMod | null>(
    null,
  );
  const [activeTab, setActiveTab] = useState<"installed" | "search">(
    "installed",
  );

  const { data: installed = [], isLoading: loadingInstalled } = useQuery({
    queryKey: ["mods", serverId],
    queryFn: () => api.mods.list(serverId),
  });

  const { data: searchResult, isFetching: searching } = useQuery({
    queryKey: ["mod-search", serverId, query, loader, mcVersion],
    queryFn: () => api.mods.search(serverId, query, loader, mcVersion),
    enabled: query.length >= 2,
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
      {/* Search bar */}
      <div className="px-4 py-3 border-b border-border bg-surface">
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
      </div>

      {/* Tab bar */}
      <div className="flex border-b border-border bg-surface">
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
              loader={loader}
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
