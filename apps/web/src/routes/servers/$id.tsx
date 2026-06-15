import { createRoute, useParams, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Play,
  Square,
  RotateCcw,
  Skull,
  ArrowLeft,
  HardDrive,
  LayoutDashboard,
  Terminal,
  FileText,
  Users,
  PackageOpen,
  FolderTree,
  Globe2,
  SlidersHorizontal,
  FileCog,
  ToggleRight,
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
import { type ServerSection } from "@/components/servers/shared";
import { BackupsTab } from "@/components/servers/backups-tab";
import { DashboardTab } from "@/components/servers/dashboard-tab";
import { TasksTab } from "@/components/servers/tasks-tab";
import { LogsTab } from "@/components/servers/logs-tab";
import { WorldsTab } from "@/components/servers/worlds-tab";
import { OptionsTab, PropertiesTab } from "@/components/servers/options-properties";

const serverSections: Array<{
  value: ServerSection;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
}> = [
  { value: "dashboard", label: "Dashboard", icon: LayoutDashboard },
  { value: "console", label: "Console", icon: Terminal },
  { value: "logs", label: "Logs", icon: FileText },
  { value: "players", label: "Players", icon: Users },
  { value: "mods", label: "Mods", icon: PackageOpen },
  { value: "options", label: "Options", icon: SlidersHorizontal },
  { value: "properties", label: "Properties", icon: FileCog },
  { value: "configs", label: "Configs", icon: FileCog },
  { value: "files", label: "Files", icon: FolderTree },
  { value: "worlds", label: "Worlds", icon: Globe2 },
  { value: "backups", label: "Backups", icon: HardDrive },
  { value: "tasks", label: "Tasks", icon: ToggleRight },
];

// ── Main page ─────────────────────────────────────────────────────────────────

function ServerDetailPage() {
  const { id } = useParams({ from: "/servers/$id" });
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { error } = useNotifications();
  const [tab, setTab] = useState<ServerSection>(() => {
    const stored = sessionStorage.getItem(`server:${id}:tab`);
    if (stored) sessionStorage.removeItem(`server:${id}:tab`);
    if (stored === "software") return "options";
    return (stored as ServerSection | null) ?? "dashboard";
  });
  const [selectedFile, setSelectedFile] = useState<string | null>(null);

  const { data: server, isLoading } = useQuery({
    queryKey: ["server", id],
    queryFn: () => api.servers.get(id),
    refetchInterval: 8_000,
  });

  const { data: backups = [] } = useQuery({
    queryKey: ["backups", id],
    queryFn: () => api.backups.list(id),
    refetchInterval: 10_000,
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

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="flex items-center gap-2 sm:gap-4 px-3 sm:px-6 py-3 border-b border-border bg-surface/50 flex-shrink-0">
        <Button
          variant="ghost"
          size="icon"
          onClick={() => navigate({ to: "/servers" })}
          title="Back to servers"
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
          <ResourceChart serverId={id} />
        </div>

        {/* Controls */}
        <div className="flex items-center gap-1.5 flex-shrink-0">
          {!isOnline ? (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => start.mutate()}
              loading={busy}
              title="Start"
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
              >
                <RotateCcw className="h-4 w-4 text-yellow-400" />
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => stop.mutate()}
                loading={busy}
                title="Stop"
              >
                <Square className="h-4 w-4 text-red-400" />
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => kill.mutate()}
                loading={busy}
                title="Kill"
              >
                <Skull className="h-4 w-4 text-red-600" />
              </Button>
            </>
          )}
        </div>
      </div>

      <div className="flex flex-1 min-h-0 flex-col md:flex-row">
        <aside className="flex-shrink-0 border-b border-border bg-surface/40 p-2 md:w-56 md:border-b-0 md:border-r md:p-3">
          <nav className="scrollbar-none flex gap-1 overflow-x-auto md:flex-col md:overflow-visible">
            {serverSections.map((section) => {
              const Icon = section.icon;
              const active = tab === section.value;
              return (
                <button
                  key={section.value}
                  onClick={() => setTab(section.value)}
                  className={`flex h-9 flex-shrink-0 items-center gap-2 rounded-md px-3 text-left text-sm transition-colors md:w-full ${
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
          </nav>
        </aside>

        <main className="flex-1 min-w-0 min-h-0 overflow-hidden">
          {tab === "dashboard" && (
            <div className="h-full overflow-y-auto p-4 sm:p-6">
              <DashboardTab
                server={server}
                backups={backups}
                onSection={setTab}
              />
            </div>
          )}
          {tab === "console" && (
            <div className="h-full min-h-0 p-4 pb-6">
              <ServerTerminal serverId={id} />
            </div>
          )}
          {tab === "logs" && <LogsTab serverId={id} />}
          {tab === "players" && (
            <PlayersPanel
              serverId={id}
              status={server.status as ServerStatus}
            />
          )}
          {tab === "options" && (
            <div className="h-full overflow-y-auto p-4 sm:p-6">
              <OptionsTab server={server} />
            </div>
          )}
          {tab === "properties" && (
            <div
              className="h-full overflow-y-auto px-4 pb-4 pt-0 sm:px-6 sm:pb-6 sm:pt-0"
              data-server-scroll
            >
              <PropertiesTab serverId={id} />
            </div>
          )}
          {tab === "configs" && <ConfigsTab serverId={id} />}
          {tab === "files" && (
            <div className="flex h-full min-w-0">
              {/* On mobile, show the browser OR the editor (not both); on md+ show both side by side. */}
              <div
                className={`${selectedFile ? "hidden md:flex" : "flex"} w-full flex-shrink-0 flex-col overflow-hidden border-border md:w-80 md:border-r`}
              >
                <FileBrowser
                  serverId={id}
                  onFileSelect={(path) => setSelectedFile(path)}
                />
              </div>
              <div
                className={`${selectedFile ? "flex" : "hidden md:flex"} min-w-0 flex-1 flex-col overflow-hidden`}
              >
                {selectedFile ? (
                  <>
                    <button
                      onClick={() => setSelectedFile(null)}
                      className="flex flex-shrink-0 items-center gap-1.5 border-b border-border bg-surface px-4 py-2 text-sm text-text-secondary hover:text-text-primary md:hidden"
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
          {tab === "worlds" && <WorldsTab serverId={id} />}
          {tab === "mods" && (
            <ModSearch
              serverId={id}
              loader={server.platform}
              mcVersion={server.mc_version}
              platform={server.platform}
            />
          )}
          {tab === "backups" && (
            <div className="h-full overflow-y-auto p-4 sm:p-6">
              <BackupsTab serverId={id} />
            </div>
          )}
          {tab === "tasks" && (
            <div className="h-full overflow-y-auto p-4 sm:p-6">
              <TasksTab serverId={id} />
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
  path: "/servers/$id",
  component: ServerDetailPage,
});
