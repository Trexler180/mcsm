import { useParams, useNavigate } from "@tanstack/react-router";
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Play,
  Square,
  RotateCcw,
  Skull,
  ArrowLeft,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { ModConflictDialog } from "@/components/mods/conflict-dialog";
import { ResourceChart } from "@/components/charts/resource-chart";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { ServerStatus } from "@/lib/types";
import {
  type ServerSection,
  SERVER_SECTIONS,
} from "@/components/servers/shared";
import { SectionNav } from "@/components/servers/section-nav";
import type { ServerPermission } from "@/lib/types";
import { can as hasPermission } from "@/lib/permissions";
import { ServerPermissionsProvider } from "@/lib/server-permissions";
import { PermissionButton } from "@/components/ui/permission";

const ServerTerminal = lazy(() =>
  import("@/components/console/terminal").then((m) => ({ default: m.ServerTerminal })),
);
const FileBrowser = lazy(() =>
  import("@/components/files/browser").then((m) => ({ default: m.FileBrowser })),
);
const FileEditor = lazy(() =>
  import("@/components/files/editor").then((m) => ({ default: m.FileEditor })),
);
const DatViewer = lazy(() =>
  import("@/components/files/dat-viewer").then((m) => ({ default: m.DatViewer })),
);
const ModSearch = lazy(() =>
  import("@/components/mods/search").then((m) => ({ default: m.ModSearch })),
);
const PlayersPanel = lazy(() =>
  import("@/components/players/panel").then((m) => ({ default: m.PlayersPanel })),
);
const ConfigsTab = lazy(() =>
  import("@/components/configs/configs-tab").then((m) => ({ default: m.ConfigsTab })),
);
const BackupsTab = lazy(() =>
  import("@/components/servers/backups-tab").then((m) => ({ default: m.BackupsTab })),
);
const DashboardTab = lazy(() =>
  import("@/components/servers/dashboard-tab").then((m) => ({ default: m.DashboardTab })),
);
const StatsTab = lazy(() =>
  import("@/components/servers/stats-tab").then((m) => ({ default: m.StatsTab })),
);
const TasksTab = lazy(() =>
  import("@/components/servers/tasks-tab").then((m) => ({ default: m.TasksTab })),
);
const LogsTab = lazy(() =>
  import("@/components/servers/logs-tab").then((m) => ({ default: m.LogsTab })),
);
const WorldsTab = lazy(() =>
  import("@/components/servers/worlds-tab").then((m) => ({ default: m.WorldsTab })),
);
const OptionsTab = lazy(() =>
  import("@/components/servers/options/options-tab").then((m) => ({ default: m.OptionsTab })),
);
const PropertiesTab = lazy(() =>
  import("@/components/servers/options/properties-panel").then((m) => ({ default: m.PropertiesTab })),
);
const VersionTab = lazy(() =>
  import("@/components/servers/version-migration").then((m) => ({ default: m.VersionTab })),
);
const AccessTab = lazy(() =>
  import("@/components/servers/access-tab").then((m) => ({ default: m.AccessTab })),
);

// ── Main page ─────────────────────────────────────────────────────────────────

// Shown while the server row loads. Mirrors the real shell — header bar, section
// nav, stat tiles and content panels — so the layout holds instead of collapsing
// to a centered spinner and jumping when data arrives.
function ServerShellSkeleton() {
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b border-border bg-surface/50 px-3 py-3 sm:gap-4 sm:px-6">
        <Skeleton className="h-8 w-8 flex-shrink-0 rounded-md" />
        <div className="flex-1 space-y-2">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="h-3 w-56" />
        </div>
        <Skeleton className="hidden h-10 w-64 lg:block" />
        <Skeleton className="h-8 w-20 flex-shrink-0" />
      </div>

      <div className="flex flex-1 flex-col md:flex-row">
        <aside className="flex-shrink-0 space-y-1.5 border-b border-border bg-surface/40 p-2 md:w-48 md:border-b-0 md:border-r md:p-3 lg:w-56">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-8 w-full" />
          ))}
        </aside>

        <main className="flex-1 space-y-5 p-4 sm:p-6">
          <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-20" />
            ))}
          </div>
          <div className="grid grid-cols-1 gap-5 lg:grid-cols-[1.3fr_1fr]">
            <Skeleton className="h-56" />
            <Skeleton className="h-56" />
          </div>
        </main>
      </div>
    </div>
  );
}

function SectionFallback() {
  return (
    <div className="h-full space-y-4 overflow-hidden p-4 sm:p-6">
      <Skeleton className="h-8 w-48" />
      <Skeleton className="h-40 w-full" />
      <Skeleton className="h-40 w-full" />
    </div>
  );
}

const validSections = new Set<string>(SERVER_SECTIONS.map((s) => s.value));

export function ServerDetailPage() {
  const { id, section } = useParams({ from: "/servers/$id/$section" });
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { error } = useNotifications();
  // The active tab lives in the URL (/servers/:id/:section) so every tab is
  // linkable and deep-links (e.g. from the dashboard) land on the right tab.
  // "software" is a legacy alias kept working; anything unknown falls back to
  // the dashboard.
  const tab: ServerSection = validSections.has(section)
    ? (section as ServerSection)
    : section === "software"
      ? "options"
      : "dashboard";
  const setTab = (next: ServerSection) =>
    navigate({ to: "/servers/$id/$section", params: { id, section: next } });
  const [selectedFile, setSelectedFile] = useState<string | null>(null);

  const { data: permissions } = useQuery({
    queryKey: ["server-permissions", id],
    queryFn: () => api.servers.myPermissions(id),
  });
  const can = (permission: ServerPermission) => hasPermission(permissions, permission);
  const allowedSections = useMemo(
    () => SERVER_SECTIONS.filter((section) => hasPermission(permissions, section.permission)),
    [permissions],
  );
  const allowedSectionValues = useMemo(
    () => new Set(allowedSections.map((section) => section.value)),
    [allowedSections],
  );
  const sectionGroups = useMemo(
    () =>
      allowedSections.reduce<Array<{ group: string; items: typeof SERVER_SECTIONS }>>(
        (acc, section) => {
          const existing = acc.find((g) => g.group === section.group);
          if (existing) existing.items.push(section);
          else acc.push({ group: section.group, items: [section] });
          return acc;
        },
        [],
      ),
    [allowedSections],
  );

  const { data: server, isLoading } = useQuery({
    queryKey: ["server", id],
    queryFn: () => api.servers.get(id),
    refetchInterval: 8_000,
  });

  const { data: backups = [] } = useQuery({
    queryKey: ["backups", id],
    queryFn: () => api.backups.list(id),
    refetchInterval: 10_000,
    enabled: can("backups"),
  });

  // Live agent status carries the parsed Fabric mod-conflict (if any), which the
  // DB-backed server row doesn't include. Track the last conflict the user
  // dismissed so it doesn't immediately reappear.
  const { data: agentStatus } = useQuery({
    queryKey: ["agent-status", id],
    queryFn: () => api.servers.status(id),
    refetchInterval: 6_000,
  });
  const [dismissedConflict, setDismissedConflict] = useState<number | null>(
    null,
  );
  const conflict = agentStatus?.mod_conflict?.detected
    ? agentStatus.mod_conflict
    : null;
  const showConflict =
    conflict != null && conflict.detected_at !== dismissedConflict;

  // Persist each newly detected conflict to the backend once, so the overview
  // can surface unresolved conflicts across servers. The store de-dupes by
  // (server, summary) while a conflict is open; disabling the jars resolves it.
  const reportedConflict = useRef<number | null>(null);
  useEffect(() => {
    if (!permissions) return;
    if (!allowedSectionValues.has(tab)) {
      setTab("dashboard");
    }
  }, [allowedSectionValues, permissions, tab]);

  useEffect(() => {
    if (!conflict || conflict.detected_at === reportedConflict.current) return;
    if (!can("mods")) return;
    reportedConflict.current = conflict.detected_at;
    api.mods
      .recordConflict(id, {
        kind: conflict.kind ?? "crash",
        summary: conflict.summary,
        mods: (conflict.suggestions ?? []).map((s) => s.mod_name).filter(Boolean),
      })
      .catch(() => {
        // Best-effort: the dialog still works if recording fails.
      });
  }, [conflict, id, permissions]);

  const start = useMutation({
    mutationFn: () => api.servers.start(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["server", id] }),
    onError: (e: Error) => error("Start failed", e.message),
  });
  const stop = useMutation({
    mutationFn: () => api.servers.stop(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["server", id] }),
    onError: (e: Error) => error("Stop failed", e.message),
  });
  const restart = useMutation({
    mutationFn: () => api.servers.restart(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["server", id] }),
    onError: (e: Error) => error("Restart failed", e.message),
  });
  const kill = useMutation({
    mutationFn: () => api.servers.kill(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["server", id] }),
    onError: (e: Error) => error("Kill failed", e.message),
  });

  if (isLoading) {
    return <ServerShellSkeleton />;
  }

  if (!server) {
    return (
      <div className="p-6 text-center text-text-secondary">
        <p>Server not found</p>
        <Button className="mt-4" onClick={() => navigate({ to: "/servers" })}>
          Back to Servers
        </Button>
      </div>
    );
  }

  const isOnline = server.status === "online" || server.status === "starting";
  const busy =
    start.isPending || stop.isPending || restart.isPending || kill.isPending;
  const goSection = (next: ServerSection) => {
    if (allowedSectionValues.has(next)) setTab(next);
  };

  return (
    <ServerPermissionsProvider permissions={permissions}>
      <div className="flex flex-col h-full">
        {/* Header */}
        <div className="flex items-center gap-2 sm:gap-4 px-3 sm:px-6 py-3 border-b border-border bg-surface/50 flex-shrink-0">
          <Button
            variant="ghost"
            size="icon"
            onClick={() => navigate({ to: "/servers" })}
            title="Back to servers"
            aria-label="Back to servers"
          >
            <ArrowLeft className="h-4 w-4" />
          </Button>
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-3">
              <h1 className="text-lg font-semibold text-text-primary truncate">
                {server.name}
              </h1>
              <StatusBadge status={server.status as ServerStatus} />
            </div>
            <p className="truncate text-xs text-text-secondary">
              {server.platform} {server.mc_version} · :{server.port} ·{" "}
              {server.ram_mb_max} MB
            </p>
          </div>

          {/* Resource metrics */}
          <div className="hidden lg:block w-64">
            <ResourceChart
              serverId={id}
              ramMaxMb={server.ram_mb_max}
              status={server.status}
            />
          </div>

          {/* Controls. Each lifecycle action is its own permission, so the four
              buttons are gated one by one — holding power.start alone must not
              light up Stop and Kill. The row itself appears for anyone with some
              power access; without any, it stays out of the header entirely. */}
          {can("power") && (
            <div className="flex items-center gap-1.5 flex-shrink-0">
              {!isOnline ? (
                <PermissionButton
                  need="power.start"
                  size="sm"
                  variant="ghost"
                  onClick={() => start.mutate()}
                  loading={busy}
                  title="Start"
                  aria-label="Start server"
                >
                  <Play className="h-4 w-4 text-green-400" />
                </PermissionButton>
              ) : (
                <>
                  <PermissionButton
                    need="power.restart"
                    size="sm"
                    variant="ghost"
                    onClick={() => restart.mutate()}
                    loading={busy}
                    title="Restart"
                    aria-label="Restart server"
                  >
                    <RotateCcw className="h-4 w-4 text-yellow-400" />
                  </PermissionButton>
                  <PermissionButton
                    need="power.stop"
                    size="sm"
                    variant="ghost"
                    onClick={() => stop.mutate()}
                    loading={busy}
                    title="Stop"
                    aria-label="Stop server"
                  >
                    <Square className="h-4 w-4 text-red-400" />
                  </PermissionButton>
                  <PermissionButton
                    need="power.kill"
                    size="sm"
                    variant="ghost"
                    onClick={() => kill.mutate()}
                    loading={busy}
                    title="Kill"
                    aria-label="Kill server"
                  >
                    <Skull className="h-4 w-4 text-red-600" />
                  </PermissionButton>
                </>
              )}
            </div>
          )}
        </div>

        <div className="flex flex-1 min-h-0 flex-col md:flex-row">
          <aside className="flex-shrink-0 border-b border-border bg-surface/40 p-2 md:w-48 md:border-b-0 md:border-r md:p-3 lg:w-56">
            <SectionNav groups={sectionGroups} active={tab} onSelect={setTab} />
          </aside>

          <main className="flex-1 min-w-0 min-h-0 overflow-hidden">
            <Suspense fallback={<SectionFallback />}>
            {tab === "dashboard" && (
              <div className="h-full overflow-y-auto p-4 sm:p-6">
                <DashboardTab
                  server={server}
                  backups={backups}
                  can={can}
                  onSection={goSection}
                />
              </div>
            )}
            {tab === "console" && can("console") && (
              <div className="h-full min-h-0 p-4 pb-6">
                <ServerTerminal serverId={id} />
              </div>
            )}
            {tab === "logs" && can("files") && <LogsTab serverId={id} />}
            {tab === "stats" && (
              <div className="h-full overflow-y-auto p-4 sm:p-6">
                <StatsTab serverId={id} ramMaxMb={server.ram_mb_max} />
              </div>
            )}
            {tab === "players" && can("players") && (
              <PlayersPanel
                serverId={id}
                status={server.status as ServerStatus}
              />
            )}
            {tab === "version" && can("settings") && (
              <div className="h-full overflow-y-auto p-4 sm:p-6">
                <VersionTab server={server} />
              </div>
            )}
            {tab === "options" && can("settings") && (
              <div className="h-full overflow-y-auto p-4 sm:p-6">
                <OptionsTab server={server} />
              </div>
            )}
            {tab === "properties" && can("settings") && (
              <div
                className="h-full overflow-y-auto px-4 pb-4 pt-0 sm:px-6 sm:pb-6 sm:pt-0"
                data-server-scroll
              >
                <PropertiesTab server={server} />
              </div>
            )}
            {tab === "configs" && can("files") && <ConfigsTab serverId={id} />}
            {tab === "files" && can("files") && (
              <div className="flex h-full min-w-0">
                {/* Show the browser OR the editor (not both) until there's room for
                    a side-by-side split. The app + section sidebars already claim
                    ~448px, so the 80-wide browser + editor only fit from xl; below
                    that, one pane at a time with a back button. */}
                <div
                  className={`${selectedFile ? "hidden xl:flex" : "flex"} w-full flex-shrink-0 flex-col overflow-hidden border-border xl:w-80 xl:border-r`}
                >
                  <FileBrowser
                    serverId={id}
                    onFileSelect={(path) => setSelectedFile(path)}
                  />
                </div>
                <div
                  className={`${selectedFile ? "flex" : "hidden xl:flex"} min-w-0 flex-1 flex-col overflow-hidden`}
                >
                  {selectedFile ? (
                    <>
                      <button
                        onClick={() => setSelectedFile(null)}
                        className="flex flex-shrink-0 items-center gap-1.5 border-b border-border bg-surface px-4 py-2 text-sm text-text-secondary hover:text-text-primary xl:hidden"
                      >
                        <ArrowLeft className="h-4 w-4" /> Back to files
                      </button>
                      <div className="min-h-0 flex-1 overflow-hidden">
                        {/\.(dat|dat_old|nbt)$/i.test(selectedFile) ? (
                          <DatViewer serverId={id} path={selectedFile} />
                        ) : (
                          <FileEditor serverId={id} path={selectedFile} />
                        )}
                      </div>
                    </>
                  ) : (
                    <div className="flex h-full items-center justify-center text-text-secondary">
                      <p className="text-sm">Select a file to edit</p>
                    </div>
                  )}
                </div>
              </div>
            )}
            {tab === "worlds" && can("files") && (
              <WorldsTab serverId={id} status={server.status as ServerStatus} />
            )}
            {tab === "mods" && can("mods") && (
              <ModSearch
                serverId={id}
                loader={server.platform}
                mcVersion={server.mc_version}
                platform={server.platform}
              />
            )}
            {tab === "backups" && can("backups") && (
              <div className="h-full overflow-y-auto p-4 sm:p-6">
                <BackupsTab serverId={id} />
              </div>
            )}
            {tab === "tasks" && can("tasks") && (
              <div className="h-full overflow-y-auto p-4 sm:p-6">
                <TasksTab serverId={id} />
              </div>
            )}
            {tab === "access" && can("admin") && (
              <div className="h-full overflow-y-auto p-4 sm:p-6">
                <AccessTab serverId={id} />
              </div>
            )}
            </Suspense>
          </main>
        </div>

        {showConflict && conflict && (
          <ModConflictDialog
            serverId={id}
            conflict={conflict}
            onClose={() => setDismissedConflict(conflict.detected_at)}
          />
        )}
      </div>
    </ServerPermissionsProvider>
  );
}
