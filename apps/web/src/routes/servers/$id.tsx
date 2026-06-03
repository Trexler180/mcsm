import { createRoute, useParams, useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Play,
  Square,
  RotateCcw,
  Skull,
  ArrowLeft,
  Plus,
  Trash2,
  ToggleLeft,
  ToggleRight,
  HardDrive,
  Save,
  LayoutDashboard,
  Terminal,
  FileText,
  Users,
  PackageOpen,
  FolderTree,
  Globe2,
  ShieldCheck,
  SlidersHorizontal,
  FileCog,
} from "lucide-react";
import { Route as rootRoute } from "../__root";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/badge";
import { Dialog, ConfirmDialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
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
import type { Server, Backup, ScheduledTask, ServerStatus } from "@/lib/types";

type ServerSection =
  | "dashboard"
  | "console"
  | "logs"
  | "players"
  | "software"
  | "mods"
  | "options"
  | "configs"
  | "files"
  | "worlds"
  | "backups"
  | "tasks"
  | "access";

const serverSections: Array<{
  value: ServerSection;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
}> = [
  { value: "dashboard", label: "Dashboard", icon: LayoutDashboard },
  { value: "console", label: "Console", icon: Terminal },
  { value: "logs", label: "Logs", icon: FileText },
  { value: "players", label: "Players", icon: Users },
  { value: "software", label: "Software", icon: PackageOpen },
  { value: "mods", label: "Mods", icon: PackageOpen },
  { value: "options", label: "Options", icon: SlidersHorizontal },
  { value: "configs", label: "Configs", icon: FileCog },
  { value: "files", label: "Files", icon: FolderTree },
  { value: "worlds", label: "Worlds", icon: Globe2 },
  { value: "backups", label: "Backups", icon: HardDrive },
  { value: "tasks", label: "Tasks", icon: ToggleRight },
  { value: "access", label: "Access", icon: ShieldCheck },
];

function Panel({
  title,
  description,
  actions,
  children,
}: {
  title: string;
  description?: string;
  actions?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-md border border-border bg-surface">
      <div className="flex items-center justify-between gap-4 border-b border-border px-4 py-3 sm:px-5 sm:py-4">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-text-primary">{title}</h2>
          {description && (
            <p className="mt-1 text-xs text-text-secondary">{description}</p>
          )}
        </div>
        {actions}
      </div>
      <div className="p-4 sm:p-5">{children}</div>
    </section>
  );
}

function StatTile({
  label,
  value,
  detail,
}: {
  label: string;
  value: string;
  detail?: string;
}) {
  return (
    <div className="rounded-md border border-border bg-surface px-4 py-3">
      <p className="text-xs text-text-secondary">{label}</p>
      <p className="mt-1 truncate text-lg font-semibold text-text-primary">
        {value}
      </p>
      {detail && <p className="mt-1 text-xs text-text-secondary">{detail}</p>}
    </div>
  );
}

// ── Backups tab ────────────────────────────────────────────────────────────────

function BackupsTab({ serverId }: { serverId: string }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [restoreTarget, setRestoreTarget] = useState<Backup | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Backup | null>(null);

  const { data: backups = [], isLoading } = useQuery({
    queryKey: ["backups", serverId],
    queryFn: () => api.backups.list(serverId),
    refetchInterval: 10_000,
  });

  const createMutation = useMutation({
    mutationFn: () => api.backups.create(serverId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["backups", serverId] });
      success("Backup started");
    },
    onError: (e: Error) => error("Backup failed", e.message),
  });

  const restoreMutation = useMutation({
    mutationFn: (backupId: string) => api.backups.restore(serverId, backupId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["backups", serverId] });
      success("Backup restored", "Server stopped — start it to apply.");
      setRestoreTarget(null);
    },
    onError: (e: Error) => error("Restore failed", e.message),
  });

  const deleteMutation = useMutation({
    mutationFn: (backupId: string) => api.backups.delete(serverId, backupId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["backups", serverId] });
      success("Backup deleted");
      setDeleteTarget(null);
    },
    onError: (e: Error) => error("Delete failed", e.message),
  });

  const statusColor: Record<Backup["status"], string> = {
    running: "text-yellow-400",
    success: "text-green-400",
    failed: "text-red-400",
  };

  const formatBytes = (bytes: number | null) => {
    if (!bytes) return "—";
    if (bytes >= 1_073_741_824)
      return `${(bytes / 1_073_741_824).toFixed(1)} GB`;
    if (bytes >= 1_048_576) return `${(bytes / 1_048_576).toFixed(1)} MB`;
    return `${(bytes / 1024).toFixed(0)} KB`;
  };

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <p className="text-sm text-text-secondary">
          {backups.length} backup{backups.length !== 1 ? "s" : ""}
        </p>
        <Button
          size="sm"
          onClick={() => createMutation.mutate()}
          loading={createMutation.isPending}
        >
          <HardDrive className="h-3.5 w-3.5" /> Backup Now
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-8">
          <div className="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin" />
        </div>
      ) : backups.length === 0 ? (
        <div className="text-center py-12 text-text-secondary">
          <HardDrive className="h-8 w-8 mx-auto mb-2 opacity-30" />
          <p className="text-sm">No backups yet</p>
        </div>
      ) : (
        <div className="border border-border rounded-lg overflow-hidden">
          {backups.map((b, i) => (
            <div
              key={b.id}
              className={`flex items-center justify-between px-4 py-3 ${i < backups.length - 1 ? "border-b border-border/50" : ""}`}
            >
              <div>
                <p className={`text-sm font-medium ${statusColor[b.status]}`}>
                  {b.status.charAt(0).toUpperCase() + b.status.slice(1)}
                </p>
                <p className="text-xs text-text-secondary mt-0.5">
                  {new Date(b.started_at).toLocaleString()}
                  {b.trigger !== "manual" && ` · ${b.trigger}`}
                </p>
              </div>
              <div className="flex items-center gap-3">
                <div className="text-right">
                  <p className="text-sm text-text-secondary font-mono">
                    {formatBytes(b.size_bytes)}
                  </p>
                  {b.completed_at && (
                    <p className="text-xs text-text-secondary">
                      {(
                        (new Date(b.completed_at).getTime() -
                          new Date(b.started_at).getTime()) /
                        1000
                      ).toFixed(0)}
                      s
                    </p>
                  )}
                </div>
                {b.status === "success" && (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => setRestoreTarget(b)}
                    title="Restore this backup"
                  >
                    <RotateCcw className="h-3.5 w-3.5" /> Restore
                  </Button>
                )}
                {b.status !== "running" && (
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => setDeleteTarget(b)}
                    title="Delete this backup"
                  >
                    <Trash2 className="h-3.5 w-3.5 text-red-400" />
                  </Button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      <ConfirmDialog
        open={restoreTarget !== null}
        onClose={() => setRestoreTarget(null)}
        onConfirm={() =>
          restoreTarget && restoreMutation.mutate(restoreTarget.id)
        }
        title="Restore backup"
        description="This stops the server and overwrites all current files with this backup. This cannot be undone. Continue?"
        confirmLabel="Restore"
        variant="destructive"
        loading={restoreMutation.isPending}
      />

      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        title="Delete backup"
        description="Delete this backup permanently? This removes the saved zip and cannot be undone."
        confirmLabel="Delete"
        variant="destructive"
        loading={deleteMutation.isPending}
      />
    </div>
  );
}

function DashboardTab({
  server,
  backups,
  onSection,
}: {
  server: Server;
  backups: Backup[];
  onSection: (section: ServerSection) => void;
}) {
  const latestBackup = backups[0];
  const ram =
    server.ram_mb_max >= 1024 && server.ram_mb_max % 1024 === 0
      ? `${server.ram_mb_max / 1024} GB`
      : `${server.ram_mb_max} MB`;

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatTile
          label="Status"
          value={server.status}
          detail={
            server.status === "online" ? "Accepting commands" : "Not running"
          }
        />
        <StatTile
          label="Address"
          value={`:${server.port}`}
          detail="Server port"
        />
        <StatTile
          label="Memory"
          value={ram}
          detail={`Min ${server.ram_mb_min} MB`}
        />
        <StatTile
          label="Software"
          value={server.platform}
          detail={server.mc_version}
        />
      </div>

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-[1.3fr_1fr]">
        <Panel
          title="Resources"
          description="Live agent metrics for this server."
        >
          <ResourceChart serverId={server.id} />
        </Panel>
        <Panel title="Quick Actions">
          <div className="grid grid-cols-2 gap-2">
            <Button variant="outline" onClick={() => onSection("console")}>
              <Terminal className="h-4 w-4" /> Console
            </Button>
            <Button variant="outline" onClick={() => onSection("files")}>
              <FolderTree className="h-4 w-4" /> Files
            </Button>
            <Button variant="outline" onClick={() => onSection("software")}>
              <PackageOpen className="h-4 w-4" /> Software
            </Button>
            <Button variant="outline" onClick={() => onSection("backups")}>
              <HardDrive className="h-4 w-4" /> Backups
            </Button>
          </div>
        </Panel>
      </div>

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        <Panel title="Server Details">
          <dl className="grid grid-cols-[120px_1fr] gap-y-2 text-sm">
            <dt className="text-text-secondary">Name</dt>
            <dd className="text-text-primary">{server.name}</dd>
            <dt className="text-text-secondary">Directory</dt>
            <dd className="truncate font-mono text-text-primary">
              {server.directory_path}
            </dd>
            <dt className="text-text-secondary">Java</dt>
            <dd className="font-mono text-text-primary">
              {server.java_binary}
            </dd>
            <dt className="text-text-secondary">Auto Start</dt>
            <dd className="text-text-primary">
              {server.auto_start ? "Enabled" : "Disabled"}
            </dd>
          </dl>
        </Panel>
        <Panel title="Latest Backup">
          {latestBackup ? (
            <div className="space-y-2 text-sm">
              <div className="flex items-center justify-between">
                <span className="text-text-secondary">Status</span>
                <Badge
                  variant={
                    latestBackup.status === "success"
                      ? "success"
                      : latestBackup.status === "failed"
                        ? "error"
                        : "warning"
                  }
                >
                  {latestBackup.status}
                </Badge>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-text-secondary">Started</span>
                <span>
                  {new Date(latestBackup.started_at).toLocaleString()}
                </span>
              </div>
            </div>
          ) : (
            <p className="text-sm text-text-secondary">No backups yet.</p>
          )}
        </Panel>
      </div>
    </div>
  );
}

// ── Tasks tab ─────────────────────────────────────────────────────────────────

function CreateTaskDialog({
  serverId,
  open,
  onClose,
}: {
  serverId: string;
  open: boolean;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [form, setForm] = useState({
    name: "",
    cron_expr: "0 4 * * *",
    action: "command",
    command: "",
  });

  const mutation = useMutation({
    mutationFn: () =>
      api.tasks.create(serverId, {
        name: form.name,
        cron_expr: form.cron_expr,
        action: form.action,
        payload: form.action === "command" ? { command: form.command } : {},
        enabled: true,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["tasks", serverId] });
      success("Task created");
      onClose();
      setForm({
        name: "",
        cron_expr: "0 4 * * *",
        action: "command",
        command: "",
      });
    },
    onError: (e: Error) => error("Create failed", e.message),
  });

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }));

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="New Scheduled Task"
      className="max-w-md"
    >
      <div className="space-y-4">
        <div className="space-y-1.5">
          <Label>Name</Label>
          <Input
            placeholder="Daily restart"
            value={form.name}
            onChange={f("name")}
          />
        </div>
        <div className="space-y-1.5">
          <Label>Cron expression</Label>
          <Input
            placeholder="0 4 * * *"
            value={form.cron_expr}
            onChange={f("cron_expr")}
            className="font-mono"
          />
          <p className="text-xs text-text-secondary">
            Format: minute hour day month weekday
          </p>
        </div>
        <div className="space-y-1.5">
          <Label>Action</Label>
          <select
            className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
            value={form.action}
            onChange={f("action")}
          >
            <option value="command">Run command</option>
            <option value="restart">Restart server</option>
            <option value="stop">Stop server</option>
            <option value="backup">Create backup</option>
          </select>
        </div>
        {form.action === "command" && (
          <div className="space-y-1.5">
            <Label>Command</Label>
            <Input
              placeholder="say Server restarting in 1 minute"
              value={form.command}
              onChange={f("command")}
              className="font-mono"
            />
          </div>
        )}
        <div className="flex justify-end gap-3 pt-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            onClick={() => mutation.mutate()}
            loading={mutation.isPending}
          >
            Create
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

function TasksTab({ serverId }: { serverId: string }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [showCreate, setShowCreate] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<ScheduledTask | null>(null);

  const { data: tasks = [], isLoading } = useQuery({
    queryKey: ["tasks", serverId],
    queryFn: () => api.tasks.list(serverId),
  });

  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      api.tasks.update(serverId, id, { enabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tasks", serverId] }),
    onError: (e: Error) => error("Update failed", e.message),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.tasks.delete(serverId, id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["tasks", serverId] });
      success("Task deleted");
      setDeleteTarget(null);
    },
    onError: (e: Error) => error("Delete failed", e.message),
  });

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <p className="text-sm text-text-secondary">
          {tasks.length} task{tasks.length !== 1 ? "s" : ""}
        </p>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="h-3.5 w-3.5" /> New Task
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-8">
          <div className="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin" />
        </div>
      ) : tasks.length === 0 ? (
        <div className="text-center py-12 text-text-secondary">
          <p className="text-sm">No scheduled tasks</p>
        </div>
      ) : (
        <div className="border border-border rounded-lg overflow-hidden">
          {tasks.map((task, i) => (
            <div
              key={task.id}
              className={`flex items-center gap-4 px-4 py-3 ${i < tasks.length - 1 ? "border-b border-border/50" : ""}`}
            >
              <button
                onClick={() =>
                  toggleMutation.mutate({ id: task.id, enabled: !task.enabled })
                }
                className="flex-shrink-0"
                title={task.enabled ? "Disable" : "Enable"}
              >
                {task.enabled ? (
                  <ToggleRight className="h-5 w-5 text-accent" />
                ) : (
                  <ToggleLeft className="h-5 w-5 text-text-secondary" />
                )}
              </button>
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium text-text-primary">
                  {task.name}
                </p>
                <p className="text-xs text-text-secondary font-mono mt-0.5">
                  {task.cron_expr} · {task.action}
                  {task.payload?.command
                    ? `: ${task.payload.command as string}`
                    : ""}
                </p>
              </div>
              <div className="text-right text-xs text-text-secondary flex-shrink-0">
                {task.next_run && (
                  <p>Next: {new Date(task.next_run).toLocaleString()}</p>
                )}
                {task.last_run && (
                  <p>Last: {new Date(task.last_run).toLocaleString()}</p>
                )}
              </div>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => setDeleteTarget(task)}
              >
                <Trash2 className="h-3.5 w-3.5 text-red-400" />
              </Button>
            </div>
          ))}
        </div>
      )}

      <CreateTaskDialog
        serverId={serverId}
        open={showCreate}
        onClose={() => setShowCreate(false)}
      />

      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        title="Delete task"
        description={`Delete "${deleteTarget?.name}"?`}
        confirmLabel="Delete"
        variant="destructive"
        loading={deleteMutation.isPending}
      />
    </div>
  );
}

// ── Settings tab ──────────────────────────────────────────────────────────────

type PropertiesMap = Record<string, string>;

const propertyFields = [
  { key: "motd", label: "MOTD", type: "text" },
  {
    key: "gamemode",
    label: "Game Mode",
    type: "select",
    options: ["survival", "creative", "adventure", "spectator"],
  },
  {
    key: "difficulty",
    label: "Difficulty",
    type: "select",
    options: ["peaceful", "easy", "normal", "hard"],
  },
  { key: "max-players", label: "Max Players", type: "number" },
  { key: "server-port", label: "Server Port", type: "number" },
  { key: "view-distance", label: "View Distance", type: "number" },
  { key: "simulation-distance", label: "Simulation Distance", type: "number" },
  { key: "online-mode", label: "Online Mode", type: "boolean" },
  { key: "pvp", label: "PVP", type: "boolean" },
  { key: "allow-flight", label: "Allow Flight", type: "boolean" },
  { key: "enable-command-block", label: "Command Blocks", type: "boolean" },
] as const;

const defaultServerProperties = `# Minecraft server properties
motd=A Minecraft Server
gamemode=survival
difficulty=normal
max-players=20
server-port=25565
view-distance=10
simulation-distance=10
online-mode=true
pvp=true
allow-flight=false
enable-command-block=false
`;

function parseProperties(content: string): PropertiesMap {
  const out: PropertiesMap = {};
  for (const line of content.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const idx = line.indexOf("=");
    if (idx === -1) continue;
    out[line.slice(0, idx).trim()] = line.slice(idx + 1);
  }
  return out;
}

function serializeProperties(original: string, values: PropertiesMap): string {
  const seen = new Set<string>();
  const lines = original.split(/\r?\n/).map((line) => {
    const trimmed = line.trim();
    const idx = line.indexOf("=");
    if (!trimmed || trimmed.startsWith("#") || idx === -1) return line;
    const key = line.slice(0, idx).trim();
    if (!(key in values)) return line;
    seen.add(key);
    return `${key}=${values[key]}`;
  });

  const missing = Object.entries(values)
    .filter(([key]) => !seen.has(key))
    .map(([key, value]) => `${key}=${value}`);

  return [...lines, ...missing].join("\n");
}

function ServerPropertiesPanel({ serverId }: { serverId: string }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [values, setValues] = useState<PropertiesMap>({});

  const propsQuery = useQuery({
    queryKey: ["file-content", serverId, "/server.properties"],
    queryFn: () => api.files.readContent(serverId, "/server.properties"),
    retry: false,
  });

  const original = propsQuery.data ?? "";

  useEffect(() => {
    if (propsQuery.data) {
      setValues(parseProperties(propsQuery.data));
    }
  }, [propsQuery.data]);

  const saveMutation = useMutation({
    mutationFn: () =>
      api.files.writeContent(
        serverId,
        "/server.properties",
        serializeProperties(original, values),
      ),
    onSuccess: () => {
      qc.invalidateQueries({
        queryKey: ["file-content", serverId, "/server.properties"],
      });
      success("server.properties saved");
    },
    onError: (e: Error) => error("Save failed", e.message),
  });

  const createMutation = useMutation({
    mutationFn: () =>
      api.files.writeContent(
        serverId,
        "/server.properties",
        defaultServerProperties,
      ),
    onSuccess: () => {
      qc.invalidateQueries({
        queryKey: ["file-content", serverId, "/server.properties"],
      });
      success("server.properties created");
    },
    onError: (e: Error) => error("Create failed", e.message),
  });

  const setProp = (key: string, value: string) => {
    setValues((prev) => ({ ...prev, [key]: value }));
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-sm font-semibold text-text-primary">
            Minecraft Properties
          </h3>
          <p className="text-xs text-text-secondary mt-1">
            Edits /server.properties on the server.
          </p>
        </div>
        <Button
          size="sm"
          onClick={() => saveMutation.mutate()}
          loading={saveMutation.isPending}
          disabled={propsQuery.isLoading || propsQuery.isError}
        >
          <Save className="h-3.5 w-3.5" /> Save Properties
        </Button>
      </div>

      {propsQuery.isLoading ? (
        <div className="flex justify-center py-8">
          <div className="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin" />
        </div>
      ) : propsQuery.isError ? (
        <div className="rounded-md border border-border bg-surface p-4 text-sm text-text-secondary">
          <div className="flex items-center justify-between gap-4">
            <span>
              server.properties was not found yet. Start the server once, or
              create a default file.
            </span>
            <Button
              size="sm"
              onClick={() => createMutation.mutate()}
              loading={createMutation.isPending}
            >
              Create
            </Button>
          </div>
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          {propertyFields.map((field) => (
            <div key={field.key} className="space-y-1.5">
              <Label>{field.label}</Label>
              {field.type === "select" ? (
                <select
                  className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
                  value={values[field.key] ?? ""}
                  onChange={(e) => setProp(field.key, e.target.value)}
                >
                  <option value="">Default</option>
                  {field.options.map((option) => (
                    <option key={option} value={option}>
                      {option}
                    </option>
                  ))}
                </select>
              ) : field.type === "boolean" ? (
                <label className="flex h-9 items-center gap-2 rounded-md border border-border bg-surface-2 px-3 text-sm">
                  <input
                    type="checkbox"
                    checked={(values[field.key] ?? "false") === "true"}
                    onChange={(e) =>
                      setProp(field.key, e.target.checked ? "true" : "false")
                    }
                  />
                  <span className="text-text-secondary">
                    {(values[field.key] ?? "false") === "true"
                      ? "Enabled"
                      : "Disabled"}
                  </span>
                </label>
              ) : (
                <Input
                  type={field.type === "number" ? "number" : "text"}
                  value={values[field.key] ?? ""}
                  onChange={(e) => setProp(field.key, e.target.value)}
                />
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// VersionSelect shows a dropdown of known versions but keeps a "Custom…" escape
// hatch (and always preserves the current value even if it's not in the list).
function VersionSelect({
  value,
  onChange,
  options,
  loading,
  placeholder,
}: {
  value: string;
  onChange: (v: string) => void;
  options: string[];
  loading?: boolean;
  placeholder?: string;
}) {
  const known = options.includes(value);
  const [custom, setCustom] = useState(!known && value !== "");

  if (custom) {
    return (
      <div className="flex gap-1.5">
        <Input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          className="font-mono"
        />
        <Button
          size="sm"
          variant="ghost"
          onClick={() => {
            setCustom(false);
            onChange(options[0] ?? "");
          }}
          title="Pick from list"
        >
          List
        </Button>
      </div>
    );
  }

  return (
    <select
      className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
      value={value}
      onChange={(e) => {
        if (e.target.value === "__custom__") {
          setCustom(true);
          return;
        }
        onChange(e.target.value);
      }}
    >
      {loading && <option>Loading…</option>}
      {!known && value !== "" && <option value={value}>{value}</option>}
      {options.map((o) => (
        <option key={o} value={o}>
          {o}
        </option>
      ))}
      <option value="__custom__">Custom…</option>
    </select>
  );
}

function SoftwareTab({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [snapshots, setSnapshots] = useState(false);
  const [form, setForm] = useState({
    platform: server.platform,
    mc_version: server.mc_version,
    loader_version: server.loader_version ?? "",
    java_binary: server.java_binary,
    jvm_args: server.jvm_args.join(" "),
  });

  useEffect(() => {
    setForm({
      platform: server.platform,
      mc_version: server.mc_version,
      loader_version: server.loader_version ?? "",
      java_binary: server.java_binary,
      jvm_args: server.jvm_args.join(" "),
    });
  }, [server]);

  const versionsQuery = useQuery({
    queryKey: ["mc-versions", form.platform, snapshots],
    queryFn: () => api.minecraft.versions(form.platform, snapshots),
    staleTime: 30 * 60_000,
  });
  const loadersQuery = useQuery({
    queryKey: ["mc-loaders", form.platform],
    queryFn: () => api.minecraft.loaders(form.platform),
    staleTime: 30 * 60_000,
  });
  const mcOptions = (versionsQuery.data ?? []).map((v) => v.version);
  const loaderOptions = (loadersQuery.data ?? []).map((v) => v.version);

  const save = () =>
    api.servers.update(server.id, {
      platform: form.platform,
      mc_version: form.mc_version,
      loader_version: form.loader_version || null,
      java_binary: form.java_binary,
      jvm_args: form.jvm_args.trim() ? form.jvm_args.trim().split(/\s+/) : [],
    });

  const updateMutation = useMutation({
    mutationFn: save,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      success("Software settings saved");
    },
    onError: (e: Error) => error("Save failed", e.message),
  });

  // Save the new version then force the agent to re-fetch the runtime jar, so the
  // change actually applies (a normal save only takes effect if no jar exists).
  const reinstallMutation = useMutation({
    mutationFn: async () => {
      await save();
      await api.servers.reinstall(server.id);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      success(
        "Version applied",
        "Server stopped and runtime reinstalled — start it to run the new version.",
      );
    },
    onError: (e: Error) => error("Reinstall failed", e.message),
  });

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }));

  // Switching the Minecraft/loader version needs a runtime reinstall to take
  // effect; other edits (java binary, JVM args) only need a save.
  const versionChanged =
    form.platform !== server.platform ||
    form.mc_version !== server.mc_version ||
    (form.loader_version || "") !== (server.loader_version ?? "");
  const dirty =
    versionChanged ||
    form.java_binary !== server.java_binary ||
    form.jvm_args !== server.jvm_args.join(" ");

  return (
    <div className="max-w-3xl space-y-5">
      <Panel
        title="Software"
        description="Choose the server runtime and version. Changing the Minecraft or loader version reinstalls the server runtime."
        actions={
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => updateMutation.mutate()}
              loading={updateMutation.isPending}
              disabled={!dirty}
            >
              {!updateMutation.isPending && <Save className="h-3.5 w-3.5" />}
              {dirty ? "Save" : "Saved"}
            </Button>
            {versionChanged && (
              <Button
                size="sm"
                onClick={() => reinstallMutation.mutate()}
                loading={reinstallMutation.isPending}
                className="whitespace-nowrap"
              >
                {!reinstallMutation.isPending && (
                  <RotateCcw className="h-3.5 w-3.5" />
                )}
                Apply &amp; reinstall
              </Button>
            )}
          </div>
        }
      >
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label>Type</Label>
            <select
              className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
              value={form.platform}
              onChange={f("platform")}
            >
              {[
                "vanilla",
                "paper",
                "purpur",
                "fabric",
                "forge",
                "neoforge",
                "quilt",
                "spigot",
              ].map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1.5">
            <div className="flex items-center justify-between">
              <Label>Minecraft Version</Label>
              <label className="flex items-center gap-1.5 text-xs text-text-secondary cursor-pointer">
                <input
                  type="checkbox"
                  checked={snapshots}
                  onChange={(e) => setSnapshots(e.target.checked)}
                />
                Snapshots
              </label>
            </div>
            <VersionSelect
              value={form.mc_version}
              onChange={(v) => setForm((p) => ({ ...p, mc_version: v }))}
              options={mcOptions}
              loading={versionsQuery.isFetching}
              placeholder="1.21.4"
            />
          </div>
          <div className="space-y-1.5">
            <Label>Loader Version</Label>
            {loaderOptions.length > 0 ? (
              <VersionSelect
                value={form.loader_version}
                onChange={(v) => setForm((p) => ({ ...p, loader_version: v }))}
                options={loaderOptions}
                loading={loadersQuery.isFetching}
                placeholder="Latest compatible"
              />
            ) : (
              <Input
                value={form.loader_version}
                onChange={f("loader_version")}
                placeholder="Latest compatible"
              />
            )}
          </div>
          <div className="space-y-1.5">
            <Label>Java Binary</Label>
            <Input
              value={form.java_binary}
              onChange={f("java_binary")}
              className="font-mono"
            />
          </div>
          <div className="col-span-2 space-y-1.5">
            <Label>JVM Arguments</Label>
            <Input
              value={form.jvm_args}
              onChange={f("jvm_args")}
              className="font-mono"
              placeholder="-XX:+UseG1GC"
            />
          </div>
        </div>
      </Panel>
    </div>
  );
}

function OptionsTab({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const navigate = useNavigate();
  const [showDelete, setShowDelete] = useState(false);

  const [form, setForm] = useState({
    name: server.name,
    description: server.description ?? "",
    directory_path: server.directory_path,
    port: String(server.port),
    ram_mb_max: String(server.ram_mb_max),
    ram_mb_min: String(server.ram_mb_min),
    java_binary: server.java_binary,
  });

  const updateMutation = useMutation({
    mutationFn: () =>
      api.servers.update(server.id, {
        name: form.name,
        description: form.description || null,
        directory_path: form.directory_path,
        port: Number(form.port),
        ram_mb_max: Number(form.ram_mb_max),
        ram_mb_min: Number(form.ram_mb_min),
        java_binary: form.java_binary,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      success("Settings saved");
    },
    onError: (e: Error) => error("Save failed", e.message),
  });

  const deleteMutation = useMutation({
    mutationFn: () => api.servers.delete(server.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      navigate({ to: "/servers" });
    },
    onError: (e: Error) => error("Delete failed", e.message),
  });

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }));

  return (
    <div className="max-w-3xl space-y-8">
      <ServerPropertiesPanel serverId={server.id} />

      <Panel
        title="Panel Options"
        description="Settings stored in the control panel database."
      >
        <div className="space-y-4 max-w-lg">
          <h3 className="text-sm font-semibold text-text-primary">General</h3>
          <div className="space-y-1.5">
            <Label>Name</Label>
            <Input value={form.name} onChange={f("name")} />
          </div>
          <div className="space-y-1.5">
            <Label>Description</Label>
            <Input
              value={form.description}
              onChange={f("description")}
              placeholder="Optional…"
            />
          </div>
          <div className="space-y-1.5">
            <Label>Server Directory</Label>
            <Input
              value={form.directory_path}
              onChange={f("directory_path")}
              className="font-mono"
              placeholder="E:/mc-test"
            />
          </div>
        </div>

        <div className="space-y-4">
          <h3 className="text-sm font-semibold text-text-primary">Resources</h3>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label>Port</Label>
              <Input type="number" value={form.port} onChange={f("port")} />
            </div>
            <div className="space-y-1.5">
              <Label>Max RAM (MB)</Label>
              <Input
                type="number"
                value={form.ram_mb_max}
                onChange={f("ram_mb_max")}
              />
            </div>
            <div className="space-y-1.5">
              <Label>Min RAM (MB)</Label>
              <Input
                type="number"
                value={form.ram_mb_min}
                onChange={f("ram_mb_min")}
              />
            </div>
          </div>
        </div>

        <div className="space-y-4">
          <h3 className="text-sm font-semibold text-text-primary">Java</h3>
          <div className="space-y-1.5">
            <Label>Java Binary</Label>
            <Input
              value={form.java_binary}
              onChange={f("java_binary")}
              className="font-mono"
              placeholder="java"
            />
          </div>
        </div>

        <div className="flex justify-end gap-3">
          <Button
            onClick={() => updateMutation.mutate()}
            loading={updateMutation.isPending}
          >
            Save Changes
          </Button>
        </div>
      </Panel>

      <div className="border-t border-border pt-6 space-y-3">
        <h3 className="text-sm font-semibold text-red-400">Danger Zone</h3>
        <p className="text-sm text-text-secondary">
          Deleting a server removes it from the panel. The files on the node are
          not deleted.
        </p>
        <Button
          variant="destructive"
          size="sm"
          onClick={() => setShowDelete(true)}
        >
          <Trash2 className="h-3.5 w-3.5" /> Delete Server
        </Button>
      </div>

      <ConfirmDialog
        open={showDelete}
        onClose={() => setShowDelete(false)}
        onConfirm={() => deleteMutation.mutate()}
        title="Delete server"
        description={`Delete "${server.name}"? This cannot be undone.`}
        confirmLabel="Delete"
        variant="destructive"
        loading={deleteMutation.isPending}
      />
    </div>
  );
}

function LogsTab({ serverId }: { serverId: string }) {
  const [path, setPath] = useState("/logs/latest.log");
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ["file-content", serverId, path],
    queryFn: () => api.files.readContent(serverId, path),
    retry: false,
  });

  return (
    <div className="flex h-full min-h-0 flex-col p-4 sm:p-5">
      <div className="mb-3 flex items-center gap-2">
        <Input
          value={path}
          onChange={(e) => setPath(e.target.value)}
          className="min-w-0 flex-1 font-mono sm:max-w-[20rem]"
        />
        <Button variant="outline" onClick={() => refetch()}>
          Refresh
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto rounded-md border border-border bg-[#0f0f0f] p-4 font-mono text-xs leading-5 text-text-primary">
        {isLoading ? (
          <div className="text-text-secondary">Loading log...</div>
        ) : isError ? (
          <div className="text-text-secondary">
            No log file found at {path}.
          </div>
        ) : (
          <pre className="whitespace-pre-wrap">{data}</pre>
        )}
      </div>
    </div>
  );
}

function WorldsTab({ serverId }: { serverId: string }) {
  const { data: root, isLoading } = useQuery({
    queryKey: ["files", serverId, "/"],
    queryFn: () => api.files.list(serverId, "/"),
  });

  const worlds =
    root?.entries.filter(
      (entry) =>
        entry.type === "dir" &&
        (entry.name === "world" ||
          entry.name.toLowerCase().includes("world") ||
          entry.name.toLowerCase().includes("nether") ||
          entry.name.toLowerCase().includes("end")),
    ) ?? [];

  return (
    <div className="p-4 sm:p-6">
      <Panel
        title="Worlds"
        description="Detected world folders in the server root."
      >
        {isLoading ? (
          <div className="flex justify-center py-8">
            <div className="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin" />
          </div>
        ) : worlds.length === 0 ? (
          <div className="py-10 text-center text-sm text-text-secondary">
            No world folders detected yet.
          </div>
        ) : (
          <div className="divide-y divide-border rounded-md border border-border">
            {worlds.map((world) => (
              <div
                key={world.name}
                className="flex items-center justify-between px-4 py-3"
              >
                <div className="flex items-center gap-3">
                  <Globe2 className="h-4 w-4 text-text-secondary" />
                  <div>
                    <p className="text-sm font-medium text-text-primary">
                      {world.name}
                    </p>
                    <p className="text-xs text-text-secondary">
                      Modified {new Date(world.modified).toLocaleString()}
                    </p>
                  </div>
                </div>
                <Badge variant="muted">folder</Badge>
              </div>
            ))}
          </div>
        )}
      </Panel>
    </div>
  );
}

function AccessTab() {
  return (
    <div className="p-4 sm:p-6">
      <Panel
        title="Access"
        description="Per-server collaborators and permissions will live here."
      >
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          {["Console", "Files", "Backups", "Mods", "Startup", "Settings"].map(
            (permission) => (
              <label
                key={permission}
                className="flex h-10 items-center gap-2 rounded-md border border-border bg-surface-2 px-3 text-sm text-text-secondary"
              >
                <input type="checkbox" disabled />
                {permission}
              </label>
            ),
          )}
        </div>
        <p className="mt-4 text-sm text-text-secondary">
          The database already has a server permissions table, but the
          user-facing API for assigning collaborators has not been built yet.
        </p>
      </Panel>
    </div>
  );
}

// ── Main page ─────────────────────────────────────────────────────────────────

function ServerDetailPage() {
  const { id } = useParams({ from: "/servers/$id" });
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { error } = useNotifications();
  const [tab, setTab] = useState<ServerSection>(() => {
    const stored = sessionStorage.getItem(`server:${id}:tab`);
    if (stored) sessionStorage.removeItem(`server:${id}:tab`);
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
          <nav className="flex gap-1 overflow-x-auto md:flex-col md:overflow-visible">
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
          {tab === "software" && (
            <div className="h-full overflow-y-auto p-4 sm:p-6">
              <SoftwareTab server={server} />
            </div>
          )}
          {tab === "options" && (
            <div className="h-full overflow-y-auto p-4 sm:p-6">
              <OptionsTab server={server} />
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
          {tab === "access" && <AccessTab />}
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
