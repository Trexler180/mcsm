import {
  createRoute,
  redirect,
  useParams,
  useNavigate,
} from "@tanstack/react-router";
import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Play,
  Square,
  RotateCcw,
  Skull,
  ArrowLeft,
} from "lucide-react";
import { Route as rootRoute } from "../__root";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/badge";
import { ServerTerminal } from "@/components/console/terminal";
import { FileBrowser } from "@/components/files/browser";
import { FileEditor } from "@/components/files/editor";
import { DatViewer } from "@/components/files/dat-viewer";
import { ModSearch } from "@/components/mods/search";
import { ModConflictDialog } from "@/components/mods/conflict-dialog";
import { PlayersPanel } from "@/components/players/panel";
import { ConfigsTab } from "@/components/configs/configs-tab";
import { ResourceChart } from "@/components/charts/resource-chart";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { ServerStatus } from "@/lib/types";
import {
  type ServerSection,
  SERVER_SECTIONS,
} from "@/components/servers/shared";
import { BackupsTab } from "@/components/servers/backups-tab";
import { DashboardTab } from "@/components/servers/dashboard-tab";
import { TasksTab } from "@/components/servers/tasks-tab";
import { LogsTab } from "@/components/servers/logs-tab";
import { WorldsTab } from "@/components/servers/worlds-tab";
import { OptionsTab, PropertiesTab } from "@/components/servers/options-properties";
import { VersionTab } from "@/components/servers/version-migration";
import { AccessTab } from "@/components/servers/access-tab";
import type { ServerPermission } from "@/lib/types";
import { can as hasPermission } from "@/lib/permissions";

// ── Main page ─────────────────────────────────────────────────────────────────

const validSections = new Set<string>(SERVER_SECTIONS.map((s) => s.value));

function ServerDetailPage() {
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
  // Keep the active pill in view on the mobile tab strip when the section
  // changes (e.g. via deep link or the dashboard quick-links).
  const activeTabRef = useRef<HTMLButtonElement | null>(null);
  useEffect(() => {
    activeTabRef.current?.scrollIntoView({ inline: "center", block: "nearest" });
  }, [tab]);
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
        mods: conflict.suggestions.map((s) => s.mod_name).filter(Boolean),
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
    return (
      <div className="flex justify-center items-center h-full py-16">
        <div className="w-6 h-6 border-2 border-accent border-t-transparent rounded-full animate-spin" />
      </div>
    );
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

        {/* Controls */}
        {can("power") && (
          <div className="flex items-center gap-1.5 flex-shrink-0">
            {!isOnline ? (
              <Button
                size="sm"
                variant="ghost"
                onClick={() => start.mutate()}
                loading={busy}
                title="Start"
                aria-label="Start server"
              >
                <Play className="h-4 w-4 text-green-400" />
              </Button>
            ) : (
              <>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => restart.mutate()}
                  loading={busy}
                  title="Restart"
                  aria-label="Restart server"
                >
                  <RotateCcw className="h-4 w-4 text-yellow-400" />
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => stop.mutate()}
                  loading={busy}
                  title="Stop"
                  aria-label="Stop server"
                >
                  <Square className="h-4 w-4 text-red-400" />
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => kill.mutate()}
                  loading={busy}
                  title="Kill"
                  aria-label="Kill server"
                >
                  <Skull className="h-4 w-4 text-red-600" />
                </Button>
              </>
            )}
          </div>
        )}
      </div>

      <div className="flex flex-1 min-h-0 flex-col md:flex-row">
        <aside className="flex-shrink-0 border-b border-border bg-surface/40 p-2 md:w-44 md:border-b-0 md:border-r md:p-3 lg:w-56">
          {/* Mobile: a horizontally scrollable strip of visible tab pills (no
              dropdown). Every section is a labelled, tappable button; groups are
              separated by a thin divider, and the active tab auto-scrolls into
              view. Swipe to reach sections past the edge. */}
          <div
            className="-mx-2 overflow-x-auto px-2 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden md:hidden"
            role="tablist"
            aria-label="Server sections"
          >
            <div className="flex w-max items-center gap-1.5">
              {sectionGroups.map((g, gi) => (
                <Fragment key={g.group}>
                  {gi > 0 && (
                    <span
                      className="mx-0.5 h-5 w-px flex-shrink-0 bg-border"
                      aria-hidden="true"
                    />
                  )}
                  {g.items.map((section) => {
                    const Icon = section.icon;
                    const active = tab === section.value;
                    return (
                      <button
                        key={section.value}
                        ref={active ? activeTabRef : undefined}
                        role="tab"
                        aria-selected={active}
                        onClick={() => setTab(section.value)}
                        className={`flex h-9 flex-shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md px-3 text-sm transition-colors ${
                          active
                            ? "bg-accent/15 text-text-primary"
                            : "text-text-secondary hover:bg-surface-2 hover:text-text-primary"
                        }`}
                      >
                        <Icon className="h-4 w-4 flex-shrink-0" />
                        {section.label}
                      </button>
                    );
                  })}
                </Fragment>
              ))}
            </div>
          </div>

          {/* Desktop: vertical sidebar nav, grouped with section headings. */}
          <nav className="hidden md:flex md:flex-col md:gap-1">
            {sectionGroups.map((g) => (
              <div key={g.group} className="md:mb-1">
                <p className="px-3 pb-1 pt-2 text-[10px] font-semibold uppercase tracking-wide text-text-secondary/60">
                  {g.group}
                </p>
                {g.items.map((section) => {
                  const Icon = section.icon;
                  const active = tab === section.value;
                  return (
                    <button
                      key={section.value}
                      onClick={() => setTab(section.value)}
                      className={`flex h-9 w-full items-center gap-2 rounded-md px-3 text-left text-sm transition-colors ${
                        active
                          ? "bg-accent/15 text-text-primary"
                          : "text-text-secondary hover:bg-surface-2 hover:text-text-primary"
                      }`}
                    >
                      <Icon className="h-4 w-4 flex-shrink-0" />
                      {section.label}
                    </button>
                  );
                })}
              </div>
            ))}
          </nav>
        </aside>

        <main className="flex-1 min-w-0 min-h-0 overflow-hidden">
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
              <PropertiesTab serverId={id} />
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
          {tab === "worlds" && can("files") && <WorldsTab serverId={id} />}
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
  );
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/servers/$id/$section",
  component: ServerDetailPage,
});

// Bare /servers/:id lands on the dashboard tab, so existing links keep working.
export const ServerIndexRedirectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/servers/$id",
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/servers/$id/$section",
      params: { id: params.id, section: "dashboard" },
    });
  },
});
