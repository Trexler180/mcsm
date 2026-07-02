import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowDownAZ,
  Ban,
  Download,
  Loader2,
  Package,
  RefreshCw,
  Search,
  ShieldCheck,
  Upload,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Dialog } from "@/components/ui/dialog";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { InstalledMod, ModUpdate, ModUpdateRun } from "@/lib/types";
import { AutoUpdateBanner, type BulkUpdateProgress } from "./auto-update-banner";
import { SafeUpdateDialog } from "./safe-update-dialog";
import { InstalledModRow } from "./installed-mod-row";

interface InstalledTabProps {
  serverId: string;
  /** Whether this tab is the visible one. Content stays mounted either way so
   *  filter state and in-flight polling survive tab switches. */
  active: boolean;
  installed: InstalledMod[];
  updates: ModUpdate[];
  loadingInstalled: boolean;
  loadingUpdates: boolean;
  refreshingContent: boolean;
  onRefresh: () => void;
  onUninstall: (mod: InstalledMod) => void;
  onSwitchVersion: (mod: InstalledMod) => void;
  onShowDetails: (mod: InstalledMod) => void;
  onUploadClick: () => void;
  uploadPending: boolean;
  uploadPct: number | null;
}

export function InstalledTab({
  serverId,
  active,
  installed,
  updates,
  loadingInstalled,
  loadingUpdates,
  refreshingContent,
  onRefresh,
  onUninstall,
  onSwitchVersion,
  onShowDetails,
  onUploadClick,
  uploadPending,
  uploadPct,
}: InstalledTabProps) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();

  // Name filter for the Installed tab (independent of the Browse search box).
  const [installedFilter, setInstalledFilter] = useState("");
  // Installed status chip: all / updates / enabled / disabled.
  const [statusFilter, setStatusFilter] = useState<
    "all" | "updates" | "enabled" | "disabled"
  >("all");
  const [installedSort, setInstalledSort] = useState<"none" | "alphabetical">(
    "alphabetical",
  );
  const [bulkUpdateProgress, setBulkUpdateProgress] =
    useState<BulkUpdateProgress | null>(null);

  const updatesByMod = new Map(updates.map((u) => [u.mod_id, u]));

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

  // Installed tab: status chip + case-insensitive name/file filter.
  const filterText = installedFilter.trim().toLowerCase();
  const enabledCount = installed.filter((m) => m.enabled).length;
  const updatesCount = updates.length;
  const visibleInstalled = installed
    .filter((m) => {
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
    })
    .sort((a, b) => {
      if (installedSort !== "alphabetical") return 0;
      return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
    });

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
    <>
      {/* display:contents keeps the bars and the list as direct flex children
          of the parent column, exactly as before the tab was extracted. */}
      <div className={active ? "contents" : "hidden"}>
        {/* ── Installed: status chips + name filter, then a table ───────── */}
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
            onClick={onRefresh}
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
                onClick={onUploadClick}
                loading={uploadPending}
              >
                {!uploadPending && <Upload className="h-3.5 w-3.5" />}
                {uploadPending && uploadPct !== null
                  ? `${uploadPct}%`
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
                  onUninstall={() => onUninstall(mod)}
                  onSwitchVersion={
                    mod.source_id &&
                    (mod.source === "modrinth" ||
                      mod.source === "curseforge" ||
                      mod.source === "hangar" ||
                      mod.source === "spigotmc")
                      ? () => onSwitchVersion(mod)
                      : undefined
                  }
                  onShowDetails={
                    mod.source_id ? () => onShowDetails(mod) : undefined
                  }
                />
              ))}
            </>
          )}
        </div>
      </div>

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
    </>
  );
}
