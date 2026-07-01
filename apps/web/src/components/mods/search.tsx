import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Search,
  Package,
  Download,
  Loader2,
  Compass,
  X,
  RefreshCw,
  ArrowDownAZ,
  Upload,
  SlidersHorizontal,
  ShieldCheck,
  Ban,
  KeyRound,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ConfirmDialog, Dialog } from "@/components/ui/dialog";
import { ModDetailDialog } from "@/components/mods/detail";
import { SafeUpdateDialog } from "@/components/mods/safe-update-dialog";
import { api } from "@/lib/api";
import { sanitizeSvg } from "@/lib/sanitize";
import { useNotifications } from "@/store/notifications";
import type {
  InstalledMod,
  ModProjectType,
  ModSearchHit,
  ModSortIndex,
  ModSource,
  ModUpdateRun,
} from "@/lib/types";
import { LOADERS, PAGE_SIZE, PROJECT_TYPES, SORTS, compatible } from "./shared";
import { AutoUpdateBanner, type BulkUpdateProgress } from "./auto-update-banner";
import { VersionSwitchDialog } from "./version-switch-dialog";
import { InstalledModRow } from "./installed-mod-row";
import { SearchHitCard } from "./search-hit-card";
import { FilterSelect } from "./filter-select";

interface ModSearchProps {
  serverId: string;
  /** Server platform, doubles as the Modrinth loader facet for mods. */
  loader: string;
  mcVersion: string;
  /** Server platform, used to fetch the Minecraft version list. */
  platform: string;
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
  // Default to anything that runs on the server (client-optional included).
  const [environment, setEnvironment] = useState<string>("");
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
  // Browse filters live in a slide-in drawer on phones; static sidebar on ≥md.
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [bulkUpdateProgress, setBulkUpdateProgress] =
    useState<BulkUpdateProgress | null>(null);
  const uploadInputRef = useRef<HTMLInputElement>(null);

  // Mods filter by modloader; plugins/datapacks/etc. don't.
  const browseLoader = projectType === "mod" ? loaderFilter : "";

  // Sort vocabularies differ per source: CurseForge has no relevance/follows
  // ranking (both map to popularity server-side), Hangar's "follows" is stars,
  // Spiget's is likes (and its relevance is just download popularity).
  const sortOptions: { value: ModSortIndex; label: string }[] =
    source === "curseforge"
      ? [
          { value: "relevance", label: "Popularity" },
          { value: "downloads", label: "Downloads" },
          { value: "newest", label: "Newest" },
          { value: "updated", label: "Updated" },
        ]
      : source === "hangar"
        ? [
            { value: "relevance", label: "Relevance" },
            { value: "downloads", label: "Downloads" },
            { value: "follows", label: "Stars" },
            { value: "newest", label: "Newest" },
            { value: "updated", label: "Updated" },
          ]
        : source === "spigotmc"
          ? [
              { value: "relevance", label: "Popularity" },
              { value: "downloads", label: "Downloads" },
              { value: "follows", label: "Likes" },
              { value: "newest", label: "Newest" },
              { value: "updated", label: "Updated" },
            ]
          : SORTS;

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

  const {
    data: updates = [],
    isLoading: loadingUpdates,
    isFetching: refreshingUpdates,
  } = useQuery({
    queryKey: ["mod-updates", serverId],
    queryFn: () => api.mods.updates(serverId),
    staleTime: 5 * 60_000,
  });
  const updatesByMod = new Map(updates.map((u) => [u.mod_id, u]));
  const refreshingContent = refreshingInstalled || refreshingUpdates;

  // ── Safe auto-update: trigger a run, poll it live, surface the outcome ──
  const [dismissedRunId, setDismissedRunId] = useState<string | null>(null);
  const [showSkipped, setShowSkipped] = useState(false);
  const [showSafeUpdate, setShowSafeUpdate] = useState(false);

  const { data: updateRuns = [] } = useQuery({
    queryKey: ["mod-update-runs", serverId],
    queryFn: () => api.mods.updateRuns(serverId, 5),
    // Poll fast while a run is in flight so the banner tracks phases live.
    refetchInterval: (q) =>
      q.state.data?.some((r) => r.status === "running") ? 2500 : false,
  });
  const activeRun = updateRuns.find((r) => r.status === "running") ?? null;
  const lastRun = updateRuns[0] ?? null;
  const prevActiveRunId = useRef<string | null>(null);
  useEffect(() => {
    // When the run we were watching reaches a terminal state, the mod list and
    // available-updates list may both have changed (updates kept or reverted).
    if (prevActiveRunId.current && !activeRun) {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] });
      qc.invalidateQueries({ queryKey: ["mod-skipped", serverId] });
    }
    prevActiveRunId.current = activeRun?.id ?? null;
  }, [activeRun, qc, serverId]);
  // Show the most recent finished run until dismissed, but only if it finished
  // within the last 10 minutes (don't resurrect week-old banners on mount).
  const finishedBannerRun =
    !activeRun &&
    lastRun &&
    lastRun.status !== "running" &&
    lastRun.id !== dismissedRunId &&
    lastRun.finished_at &&
    Date.now() - new Date(lastRun.finished_at).getTime() < 10 * 60_000
      ? lastRun
      : null;
  const bannerRun = activeRun ?? finishedBannerRun;

  const { data: skippedVersions = [] } = useQuery({
    queryKey: ["mod-skipped", serverId],
    queryFn: () => api.mods.skippedVersions(serverId),
    staleTime: 60_000,
  });

  const autoUpdateMutation = useMutation({
    mutationFn: () => api.mods.autoUpdate(serverId),
    onSuccess: (run) => {
      setDismissedRunId(null);
      qc.setQueryData<ModUpdateRun[]>(["mod-update-runs", serverId], (old) => [
        run,
        ...(old ?? []),
      ]);
      qc.invalidateQueries({ queryKey: ["mod-update-runs", serverId] });
    },
    onError: (e: Error) => error("Safe update failed to start", e.message),
  });

  // Guided safe update: optionally take a backup before kicking off the run, so
  // the operator can roll back the whole server if an update misbehaves beyond
  // what the engine's own auto-revert handles.
  const startSafeUpdate = (backupFirst: boolean) => {
    setShowSafeUpdate(false);
    if (backupFirst) {
      api.backups
        .create(serverId)
        .then(() => success("Backup started", "Updating once it's queued"))
        .catch((e: Error) => error("Backup failed to start", e.message));
    }
    autoUpdateMutation.mutate();
  };

  const unskipMutation = useMutation({
    mutationFn: (v: { project_id: string; version_id: string }) =>
      api.mods.unskipVersion(serverId, v.project_id, v.version_id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mod-skipped", serverId] });
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] });
      success("Version allowed again");
    },
    onError: (e: Error) => error("Failed to allow version", e.message),
  });

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
    // Skip CurseForge requests until a key is configured — the UI shows an
    // add-a-key prompt instead, so the call would just 502.
    enabled: source !== "curseforge" || curseforgeEnabled,
  });

  const { data: categories = [] } = useQuery({
    queryKey: ["mod-categories", source, projectType],
    queryFn: () => api.mods.categories(serverId, projectType, source),
    staleTime: 60 * 60_000,
    enabled: source !== "curseforge" || curseforgeEnabled,
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

  const [customUploadPct, setCustomUploadPct] = useState<number | null>(null);
  const uploadCustomMutation = useMutation({
    mutationFn: (files: File[]) => {
      if (files.length === 0) {
        throw new Error("Choose one or more jar files.");
      }
      const invalid = files.find((file) => !file.name.toLowerCase().endsWith(".jar"));
      if (invalid) {
        throw new Error(`${invalid.name} is not a jar file.`);
      }
      return api.mods.uploadCustom(serverId, files, (p) =>
        setCustomUploadPct(
          p.total > 0 ? Math.min(100, Math.round((p.loaded / p.total) * 100)) : 0,
        ),
      );
    },
    onSuccess: (mods) => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] });
      setActiveTab("installed");
      success(
        mods.length === 1 ? "Custom jar uploaded" : "Custom jars uploaded",
        mods.map((m) => m.file_name).join(", "),
      );
    },
    onError: (e: Error) => error("Upload failed", e.message),
    onSettled: () => setCustomUploadPct(null),
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
        detailTarget.source === "spigotmc" ? "" : mcVersion,
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

  const handleCustomUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files ?? []);
    e.target.value = "";
    if (files.length > 0) {
      uploadCustomMutation.mutate(files);
    }
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
    {
      value: "updates",
      // Until the first updates fetch resolves, `updates` is [] — show an
      // ellipsis instead of a misleading "Updates (0)" that flips to (2).
      label: loadingUpdates ? "Updates (…)" : `Updates (${updatesCount})`,
    },
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
      <input
        ref={uploadInputRef}
        type="file"
        accept=".jar"
        multiple
        className="hidden"
        onChange={handleCustomUpload}
      />
      {/* Tab bar */}
      <div className="flex-shrink-0 flex border-b border-border bg-surface items-center">
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
          <Button
            size="sm"
            variant={activeTab === "installed" ? "outline" : "ghost"}
            onClick={() => uploadInputRef.current?.click()}
            loading={uploadCustomMutation.isPending}
            title="Upload custom jar"
          >
            {!uploadCustomMutation.isPending && <Upload className="h-3.5 w-3.5" />}
            {uploadCustomMutation.isPending && customUploadPct !== null
              ? `${customUploadPct}%`
              : "Upload jar"}
          </Button>
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
          <div className="flex-shrink-0 px-4 py-3 border-b border-border bg-surface flex flex-wrap items-center gap-2">
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
              variant="outline"
              onClick={() => setShowSafeUpdate(true)}
              loading={autoUpdateMutation.isPending || !!activeRun}
              disabled={
                autoUpdateMutation.isPending ||
                !!activeRun ||
                updates.length === 0
              }
              title="Update mods, restart the server, and automatically revert + blocklist any update that breaks the boot"
            >
              <ShieldCheck className="h-3.5 w-3.5" />
              {activeRun ? "Safe update running…" : "Safe update"}
            </Button>
            {skippedVersions.length > 0 && (
              <button
                onClick={() => setShowSkipped(true)}
                className="text-xs px-3 py-1 rounded-full border bg-surface-2 text-text-secondary border-border hover:text-text-primary transition-colors inline-flex items-center gap-1.5"
                title="Versions the auto-updater reverted and will not install again"
              >
                <Ban className="h-3 w-3" />
                Skipped ({skippedVersions.length})
              </button>
            )}
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
          {bannerRun && (
            <AutoUpdateBanner
              run={bannerRun}
              onDismiss={
                bannerRun.status !== "running"
                  ? () => setDismissedRunId(bannerRun.id)
                  : undefined
              }
            />
          )}

          <div className="flex-1 min-h-0 overflow-y-auto">
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
                  onClick={() => uploadInputRef.current?.click()}
                  loading={uploadCustomMutation.isPending}
                >
                  {!uploadCustomMutation.isPending && (
                    <Upload className="h-3.5 w-3.5" />
                  )}
                  {uploadCustomMutation.isPending && customUploadPct !== null
                    ? `${customUploadPct}%`
                    : "Upload jar"}
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
                <div className="grid grid-cols-[1fr_auto] xl:grid-cols-[minmax(0,1fr)_minmax(0,18rem)_auto] gap-4 px-4 py-2 border-b border-border bg-surface-2/30 text-[10px] uppercase tracking-wide text-text-secondary sticky top-0">
                  <span>Project</span>
                  <span className="hidden xl:block">Version</span>
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
                      (mod.source === "modrinth" ||
                        mod.source === "curseforge" ||
                        mod.source === "hangar" ||
                        mod.source === "spigotmc")
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
        <div className="flex flex-col xl:flex-row flex-1 min-h-0">
          {/* Main column */}
          <div className="flex-1 min-w-0 min-h-0 flex flex-col">
            <div className="flex-shrink-0 px-4 py-3 border-b border-border bg-surface space-y-2">
              <form onSubmit={handleSearch} className="flex gap-2">
                <div className="relative flex-1">
                  <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-text-secondary pointer-events-none" />
                  <Input
                    className="pl-9"
                    placeholder={
                      source === "curseforge"
                        ? "Search CurseForge…"
                        : source === "hangar"
                          ? "Search Hangar…"
                          : source === "spigotmc"
                            ? "Search SpigotMC…"
                            : "Search content…"
                    }
                    value={searchInput}
                    onChange={(e) => setSearchInput(e.target.value)}
                  />
                </div>
                <Button type="submit" size="md" loading={searching}>
                  Search
                </Button>
              </form>
              <div className="flex items-center justify-between gap-2">
                <label className="flex items-center gap-2 text-xs text-text-secondary cursor-pointer select-none">
                  <input
                    type="checkbox"
                    className="accent-accent"
                    checked={hideInstalled}
                    onChange={(e) => setHideInstalled(e.target.checked)}
                  />
                  Hide already installed content
                </label>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  className="xl:hidden flex-shrink-0"
                  onClick={() => setFiltersOpen(true)}
                >
                  <SlidersHorizontal className="h-3.5 w-3.5" />
                  Filters
                </Button>
              </div>
            </div>

            <div className="flex-1 min-h-0 overflow-y-auto p-4">
              {source === "curseforge" && !curseforgeEnabled ? (
                <div className="max-w-md mx-auto text-center py-12 space-y-3">
                  <div className="w-11 h-11 rounded-lg bg-accent/10 flex items-center justify-center mx-auto">
                    <KeyRound className="h-5 w-5 text-accent" />
                  </div>
                  <p className="text-sm text-text-primary font-medium">
                    CurseForge search needs an API key
                  </p>
                  <p className="text-sm text-text-secondary">
                    Add a CurseForge API key in Settings to search and browse
                    CurseForge. Modrinth works without one, and installed
                    CurseForge mods still update normally.
                  </p>
                  <Link
                    to="/settings"
                    className="inline-flex items-center gap-1.5 text-sm text-accent hover:underline"
                  >
                    <KeyRound className="h-3.5 w-3.5" />
                    Open Settings → Integrations
                  </Link>
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

          {/* Dim the results behind the filter drawer while it overlays. */}
          {filtersOpen && (
            <div
              className="fixed inset-0 z-30 bg-black/50 xl:hidden"
              onClick={() => setFiltersOpen(false)}
            />
          )}

          {/* Right filter sidebar — slide-in drawer until the pane is wide
              enough for a static column. The server view's two sidebars keep the
              content pane narrow until the viewport hits xl, so the static
              sidebar only earns its place there; below that it's the drawer. */}
          <aside
            className={`${
              filtersOpen
                ? "fixed inset-y-0 right-0 z-40 w-80 max-w-[85vw] shadow-2xl"
                : "hidden"
            } xl:static xl:z-auto xl:block xl:w-60 xl:max-w-none xl:shadow-none xl:flex-shrink-0 border-l border-border bg-surface overflow-y-auto p-4 space-y-4`}
          >
            <div className="flex items-center justify-between xl:hidden">
              <span className="text-sm font-medium text-text-primary">
                Filters
              </span>
              <button
                onClick={() => setFiltersOpen(false)}
                className="text-text-secondary hover:text-text-primary"
                title="Close filters"
              >
                <X className="h-5 w-5" />
              </button>
            </div>
            <FilterSelect
              label="Source"
              value={source}
              onChange={(v) => {
                setSource(v as ModSource);
                // Category filter values and available sorts differ per source.
                setSelectedCats([]);
                if (v === "curseforge" && sortIndex === "follows") {
                  setSortIndex("relevance");
                }
                // Hangar and SpigotMC host plugins exclusively.
                if (v === "hangar" || v === "spigotmc") {
                  setProjectType("plugin");
                }
              }}
              title="Mod source"
            >
              <option value="modrinth">Modrinth</option>
              <option value="curseforge">
                CurseForge{curseforgeEnabled ? "" : " (needs API key)"}
              </option>
              <option value="hangar">Hangar (PaperMC)</option>
              <option value="spigotmc">SpigotMC</option>
            </FilterSelect>

            <FilterSelect
              label="Content type"
              value={projectType}
              onChange={(v) => {
                setProjectType(v as ModProjectType);
                setSelectedCats([]);
              }}
            >
              {(source === "hangar" || source === "spigotmc"
                ? PROJECT_TYPES.filter((t) => t.value === "plugin")
                : PROJECT_TYPES
              ).map((t) => (
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
              {sortOptions.map((s) => (
                <option key={s.value} value={s.value}>
                  {s.label}
                </option>
              ))}
            </FilterSelect>

            {/* Spiget has no version facet, so the filter would silently do
                nothing on SpigotMC. */}
            {source !== "spigotmc" && (
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
            )}

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

            {/* CurseForge has no side metadata, so the environment facet only
                exists for Modrinth. */}
            {source === "modrinth" && (
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
            )}

            {/* Category tags */}
            {groupedCats.length > 0 && (
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
                        // CF filters by numeric id, Modrinth by tag name.
                        const value = c.id ?? c.name;
                        const active = selectedCats.includes(value);
                        return (
                          <button
                            key={value}
                            onClick={() => toggleCat(value)}
                            className={`inline-flex items-center gap-1 text-xs px-2 py-1 rounded border transition-colors ${
                              active
                                ? "bg-accent/15 text-accent border-accent/40"
                                : "bg-surface-2 text-text-secondary border-border hover:text-text-primary"
                            }`}
                          >
                            {c.icon.startsWith("http") ? (
                              <img
                                src={c.icon}
                                alt=""
                                className="h-3.5 w-3.5 rounded-sm object-cover"
                              />
                            ) : c.icon && sanitizeSvg(c.icon) ? (
                              <span
                                className="h-3.5 w-3.5 inline-flex items-center justify-center [&_svg]:h-full [&_svg]:w-full"
                                dangerouslySetInnerHTML={{ __html: sanitizeSvg(c.icon) }}
                              />
                            ) : null}
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
        // SpigotMC version entries inherit the resource's stale major-only
        // "tested versions", so the MC compat tag would mislead — skip it.
        mcVersion={switchTarget?.source === "spigotmc" ? "" : mcVersion}
      />

      <SafeUpdateDialog
        open={showSafeUpdate}
        updates={updates}
        onClose={() => setShowSafeUpdate(false)}
        onConfirm={startSafeUpdate}
      />

      <Dialog
        open={showSkipped}
        onClose={() => setShowSkipped(false)}
        title="Skipped versions"
      >
        <p className="text-xs text-text-secondary mb-3">
          These versions broke the server boot during a safe update, were
          reverted, and will not be auto-installed again. Allow a version again
          if the author re-released a fixed build under the same version.
        </p>
        {skippedVersions.length === 0 ? (
          <p className="text-sm text-text-secondary py-4 text-center">
            No skipped versions
          </p>
        ) : (
          <ul className="space-y-2 max-h-80 overflow-y-auto">
            {skippedVersions.map((v) => (
              <li
                key={`${v.project_id}:${v.version_id}`}
                className="flex items-center gap-3 rounded-md border border-border bg-surface-2/40 px-3 py-2"
              >
                <Ban className="h-4 w-4 text-warning shrink-0" />
                <div className="min-w-0 flex-1">
                  <div className="text-sm text-text-primary truncate">
                    {v.mod_name || v.project_id}{" "}
                    <span className="text-text-secondary">
                      {v.version || v.version_id}
                    </span>
                  </div>
                  {v.reason && (
                    <div
                      className="text-xs text-text-secondary truncate"
                      title={v.reason}
                    >
                      {v.reason}
                    </div>
                  )}
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() =>
                    unskipMutation.mutate({
                      project_id: v.project_id,
                      version_id: v.version_id,
                    })
                  }
                  loading={unskipMutation.isPending}
                >
                  Allow again
                </Button>
              </li>
            ))}
          </ul>
        )}
      </Dialog>

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
