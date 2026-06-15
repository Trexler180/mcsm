import { createRoute, useParams, useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Play,
  Square,
  RotateCcw,
  Skull,
  ArrowLeft,
  Plus,
  Pencil,
  Trash2,
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
  SlidersHorizontal,
  FileCog,
  Copy,
  Link2,
  Upload,
  Search,
  RefreshCw,
  FileWarning,
  FileArchive,
  ChevronRight,
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
  | "mods"
  | "options"
  | "properties"
  | "configs"
  | "files"
  | "worlds"
  | "backups"
  | "tasks";

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

function Panel({
  title,
  description,
  actions,
  children,
  onClick,
}: {
  title: string;
  description?: string;
  actions?: React.ReactNode;
  children: React.ReactNode;
  onClick?: () => void;
}) {
  const clickable = !!onClick;
  return (
    <section
      className={`rounded-md border border-border bg-surface ${
        clickable ? "cursor-pointer transition-colors hover:border-border-hover" : ""
      }`}
      onClick={onClick}
      role={clickable ? "button" : undefined}
      tabIndex={clickable ? 0 : undefined}
      onKeyDown={
        clickable
          ? (e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onClick();
              }
            }
          : undefined
      }
    >
      <div className="flex items-center justify-between gap-4 border-b border-border px-4 py-3 sm:px-5 sm:py-4">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-text-primary">{title}</h2>
          {description && (
            <p className="mt-1 text-xs text-text-secondary">{description}</p>
          )}
        </div>
        {actions ??
          (clickable && (
            <ChevronRight className="h-4 w-4 flex-shrink-0 text-text-secondary" />
          ))}
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
            <Button variant="outline" onClick={() => onSection("options")}>
              <SlidersHorizontal className="h-4 w-4" /> Options
            </Button>
            <Button variant="outline" onClick={() => onSection("backups")}>
              <HardDrive className="h-4 w-4" /> Backups
            </Button>
          </div>
        </Panel>
      </div>

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        <Panel
          title="Server Details"
          description="Edit name, directory, and runtime in Options."
          onClick={() => onSection("options")}
        >
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
        <Panel
          title="Latest Backup"
          description="View and manage all backups."
          onClick={() => onSection("backups")}
        >
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

const EMPTY_TASK_FORM = {
  name: "",
  cron_expr: "0 4 * * *",
  action: "command",
  command: "",
};

function TaskDialog({
  serverId,
  open,
  onClose,
  task,
}: {
  serverId: string;
  open: boolean;
  onClose: () => void;
  task?: ScheduledTask | null;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const isEdit = !!task;
  const [form, setForm] = useState(EMPTY_TASK_FORM);

  // Load the task being edited (or reset) whenever the dialog opens.
  useEffect(() => {
    if (!open) return;
    setForm(
      task
        ? {
            name: task.name,
            cron_expr: task.cron_expr,
            action: task.action,
            command: (task.payload?.command as string) ?? "",
          }
        : EMPTY_TASK_FORM,
    );
  }, [open, task]);

  const mutation = useMutation({
    mutationFn: () => {
      const data = {
        name: form.name,
        cron_expr: form.cron_expr,
        action: form.action,
        payload: form.action === "command" ? { command: form.command } : {},
      };
      return isEdit
        ? api.tasks.update(serverId, task!.id, data)
        : api.tasks.create(serverId, { ...data, enabled: true });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["tasks", serverId] });
      success(isEdit ? "Task updated" : "Task created");
      onClose();
    },
    onError: (e: Error) =>
      error(isEdit ? "Update failed" : "Create failed", e.message),
  });

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }));

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={isEdit ? "Edit Scheduled Task" : "New Scheduled Task"}
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
            <option value="mod_update">Auto-update mods (safe)</option>
          </select>
        </div>
        {form.action === "mod_update" && (
          <p className="text-xs text-text-secondary">
            Updates mods, restarts the server, and automatically reverts +
            blocklists any update that breaks the boot.
          </p>
        )}
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
            {isEdit ? "Save" : "Create"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

function formatCountdown(ms: number): string {
  if (ms <= 0) return "now";
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  const d = Math.floor(h / 24);
  return `${d}d ${h % 24}h`;
}

function TaskDetailsDialog({
  task,
  open,
  onClose,
  onEdit,
  onDelete,
}: {
  task: ScheduledTask | null;
  open: boolean;
  onClose: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  if (!task) return null;
  const command = (task.payload?.command as string) ?? "";
  return (
    <Dialog open={open} onClose={onClose} title={task.name} className="max-w-md">
      <div className="space-y-4">
        <dl className="grid grid-cols-[110px_1fr] gap-y-2.5 text-sm">
          <dt className="text-text-secondary">Status</dt>
          <dd>
            <Badge variant={task.enabled ? "success" : "muted"}>
              {task.enabled ? "Enabled" : "Disabled"}
            </Badge>
          </dd>
          <dt className="text-text-secondary">Action</dt>
          <dd className="text-text-primary">{task.action}</dd>
          {command && (
            <>
              <dt className="text-text-secondary">Command</dt>
              <dd className="break-all font-mono text-text-primary">
                {command}
              </dd>
            </>
          )}
          <dt className="text-text-secondary">Schedule</dt>
          <dd className="font-mono text-text-primary">{task.cron_expr}</dd>
          <dt className="text-text-secondary">Next run</dt>
          <dd className="text-text-primary">
            {task.next_run
              ? task.enabled
                ? `${new Date(task.next_run).toLocaleString()} (in ${formatCountdown(
                    new Date(task.next_run).getTime() - Date.now(),
                  )})`
                : new Date(task.next_run).toLocaleString()
              : "—"}
          </dd>
          <dt className="text-text-secondary">Last run</dt>
          <dd className="text-text-primary">
            {task.last_run ? new Date(task.last_run).toLocaleString() : "Never"}
          </dd>
          <dt className="text-text-secondary">Created</dt>
          <dd className="text-text-primary">
            {new Date(task.created_at).toLocaleString()}
          </dd>
        </dl>
        <div className="flex justify-end gap-2 pt-2">
          <Button variant="ghost" onClick={onDelete}>
            <Trash2 className="h-3.5 w-3.5 text-red-400" /> Delete
          </Button>
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
          <Button onClick={onEdit}>
            <Pencil className="h-3.5 w-3.5" /> Edit
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
  const [detailsTarget, setDetailsTarget] = useState<ScheduledTask | null>(null);
  const [editTarget, setEditTarget] = useState<ScheduledTask | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ScheduledTask | null>(null);

  const { data: tasks = [], isLoading } = useQuery({
    queryKey: ["tasks", serverId],
    queryFn: () => api.tasks.list(serverId),
  });

  // Ticking clock so the "executes in" countdown stays live.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

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
                type="button"
                role="switch"
                aria-checked={task.enabled}
                onClick={() =>
                  toggleMutation.mutate({ id: task.id, enabled: !task.enabled })
                }
                title={task.enabled ? "Disable" : "Enable"}
                className={`relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors ${
                  task.enabled ? "bg-accent" : "bg-surface-2 border border-border"
                }`}
              >
                <span
                  className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${
                    task.enabled ? "translate-x-[18px]" : "translate-x-0.5"
                  }`}
                />
              </button>
              <button
                type="button"
                onClick={() => setDetailsTarget(task)}
                className="min-w-0 flex-1 text-left"
                title="View details"
              >
                <p className="text-sm font-medium text-text-primary hover:text-accent">
                  {task.name}
                </p>
                <p className="text-xs text-text-secondary font-mono mt-0.5">
                  {task.cron_expr} · {task.action}
                  {task.payload?.command
                    ? `: ${task.payload.command as string}`
                    : ""}
                </p>
              </button>
              <div className="text-right text-xs text-text-secondary flex-shrink-0">
                {task.next_run &&
                  (task.enabled ? (
                    <p>
                      In{" "}
                      <span className="font-mono text-text-primary">
                        {formatCountdown(
                          new Date(task.next_run).getTime() - now,
                        )}
                      </span>
                    </p>
                  ) : (
                    <p>Disabled</p>
                  ))}
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
                onClick={() => setEditTarget(task)}
                title="Edit task"
              >
                <Pencil className="h-3.5 w-3.5" />
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => setDeleteTarget(task)}
                title="Delete task"
              >
                <Trash2 className="h-3.5 w-3.5 text-red-400" />
              </Button>
            </div>
          ))}
        </div>
      )}

      <TaskDetailsDialog
        task={detailsTarget}
        open={detailsTarget !== null}
        onClose={() => setDetailsTarget(null)}
        onEdit={() => {
          setEditTarget(detailsTarget);
          setDetailsTarget(null);
        }}
        onDelete={() => {
          setDeleteTarget(detailsTarget);
          setDetailsTarget(null);
        }}
      />

      <TaskDialog
        serverId={serverId}
        open={showCreate}
        onClose={() => setShowCreate(false)}
      />

      <TaskDialog
        serverId={serverId}
        task={editTarget}
        open={editTarget !== null}
        onClose={() => setEditTarget(null)}
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

type PropertyFieldType =
  | "boolean"
  | "list"
  | "number"
  | "select"
  | "text"
  | "textarea";

type PropertyField = {
  key: string;
  label: string;
  type: PropertyFieldType;
  category: string;
  description?: string;
  options?: readonly string[];
  placeholder?: string;
  min?: number;
  max?: number;
  step?: number;
};

const propertyCategories = [
  "Basics",
  "Access",
  "World",
  "Gameplay",
  "Players",
  "Network",
  "Resource Pack",
  "Performance",
  "Admin",
  "Advanced",
  "Other",
] as const;

const propertyFields: PropertyField[] = [
  {
    key: "motd",
    label: "MOTD",
    type: "text",
    category: "Basics",
    placeholder: "A Minecraft Server",
  },
  {
    key: "level-name",
    label: "World Folder",
    type: "text",
    category: "Basics",
    placeholder: "world",
  },
  {
    key: "server-port",
    label: "Server Port",
    type: "number",
    category: "Basics",
    min: 1,
    max: 65535,
  },
  {
    key: "server-ip",
    label: "Bind IP",
    type: "text",
    category: "Basics",
    placeholder: "Leave blank for all interfaces",
  },
  {
    key: "online-mode",
    label: "Online Mode",
    type: "boolean",
    category: "Access",
  },
  {
    key: "white-list",
    label: "Whitelist",
    type: "boolean",
    category: "Access",
  },
  {
    key: "enforce-whitelist",
    label: "Enforce Whitelist",
    type: "boolean",
    category: "Access",
  },
  {
    key: "enforce-secure-profile",
    label: "Secure Profiles",
    type: "boolean",
    category: "Access",
  },
  {
    key: "prevent-proxy-connections",
    label: "Prevent Proxy Connections",
    type: "boolean",
    category: "Access",
  },
  {
    key: "accepts-transfers",
    label: "Accept Transfers",
    type: "boolean",
    category: "Access",
  },
  {
    key: "hide-online-players",
    label: "Hide Online Players",
    type: "boolean",
    category: "Access",
  },
  {
    key: "gamemode",
    label: "Game Mode",
    type: "select",
    category: "World",
    options: ["survival", "creative", "adventure", "spectator"],
  },
  {
    key: "difficulty",
    label: "Difficulty",
    type: "select",
    category: "World",
    options: ["peaceful", "easy", "normal", "hard"],
  },
  {
    key: "level-seed",
    label: "World Seed",
    type: "text",
    category: "World",
  },
  {
    key: "level-type",
    label: "World Type",
    type: "select",
    category: "World",
    options: [
      "minecraft:normal",
      "minecraft:flat",
      "minecraft:large_biomes",
      "minecraft:amplified",
      "minecraft:single_biome_surface",
    ],
  },
  {
    key: "generator-settings",
    label: "Generator Settings",
    type: "textarea",
    category: "World",
    placeholder: "{}",
  },
  {
    key: "generate-structures",
    label: "Generate Structures",
    type: "boolean",
    category: "World",
  },
  {
    key: "allow-nether",
    label: "Allow Nether",
    type: "boolean",
    category: "World",
  },
  {
    key: "max-world-size",
    label: "Max World Size",
    type: "number",
    category: "World",
    min: 1,
    max: 29999984,
  },
  {
    key: "hardcore",
    label: "Hardcore",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "pvp",
    label: "PVP",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "spawn-animals",
    label: "Spawn Animals",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "spawn-monsters",
    label: "Spawn Monsters",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "spawn-npcs",
    label: "Spawn NPCs",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "allow-flight",
    label: "Allow Flight",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "force-gamemode",
    label: "Force Game Mode",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "enable-command-block",
    label: "Command Blocks",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "spawn-protection",
    label: "Spawn Protection",
    type: "number",
    category: "Gameplay",
    min: 0,
  },
  {
    key: "max-players",
    label: "Max Players",
    type: "number",
    category: "Players",
    min: 1,
  },
  {
    key: "player-idle-timeout",
    label: "Idle Timeout",
    type: "number",
    category: "Players",
    min: 0,
  },
  {
    key: "op-permission-level",
    label: "OP Permission Level",
    type: "number",
    category: "Players",
    min: 1,
    max: 4,
  },
  {
    key: "function-permission-level",
    label: "Function Permission Level",
    type: "number",
    category: "Players",
    min: 1,
    max: 4,
  },
  {
    key: "enable-status",
    label: "Show Server Status",
    type: "boolean",
    category: "Network",
  },
  {
    key: "enable-query",
    label: "Query",
    type: "boolean",
    category: "Network",
  },
  {
    key: "query.port",
    label: "Query Port",
    type: "number",
    category: "Network",
    min: 1,
    max: 65535,
  },
  {
    key: "enable-rcon",
    label: "RCON",
    type: "boolean",
    category: "Network",
  },
  {
    key: "rcon.port",
    label: "RCON Port",
    type: "number",
    category: "Network",
    min: 1,
    max: 65535,
  },
  {
    key: "rcon.password",
    label: "RCON Password",
    type: "text",
    category: "Network",
  },
  {
    key: "network-compression-threshold",
    label: "Compression Threshold",
    type: "number",
    category: "Network",
    min: -1,
  },
  {
    key: "rate-limit",
    label: "Rate Limit",
    type: "number",
    category: "Network",
    min: 0,
  },
  {
    key: "use-native-transport",
    label: "Native Transport",
    type: "boolean",
    category: "Network",
  },
  {
    key: "resource-pack",
    label: "Resource Pack URL",
    type: "text",
    category: "Resource Pack",
  },
  {
    key: "resource-pack-id",
    label: "Resource Pack ID",
    type: "text",
    category: "Resource Pack",
    placeholder: "UUID",
  },
  {
    key: "resource-pack-sha1",
    label: "Resource Pack SHA-1",
    type: "text",
    category: "Resource Pack",
  },
  {
    key: "require-resource-pack",
    label: "Require Resource Pack",
    type: "boolean",
    category: "Resource Pack",
  },
  {
    key: "resource-pack-prompt",
    label: "Resource Pack Prompt",
    type: "textarea",
    category: "Resource Pack",
  },
  {
    key: "view-distance",
    label: "View Distance",
    type: "number",
    category: "Performance",
    min: 2,
    max: 32,
  },
  {
    key: "simulation-distance",
    label: "Simulation Distance",
    type: "number",
    category: "Performance",
    min: 2,
    max: 32,
  },
  {
    key: "entity-broadcast-range-percentage",
    label: "Entity Broadcast Range",
    type: "number",
    category: "Performance",
    min: 10,
    max: 1000,
  },
  {
    key: "max-tick-time",
    label: "Max Tick Time",
    type: "number",
    category: "Performance",
    min: -1,
  },
  {
    key: "sync-chunk-writes",
    label: "Sync Chunk Writes",
    type: "boolean",
    category: "Performance",
  },
  {
    key: "pause-when-empty-seconds",
    label: "Pause When Empty",
    type: "number",
    category: "Performance",
    min: 0,
  },
  {
    key: "max-chained-neighbor-updates",
    label: "Max Neighbor Updates",
    type: "number",
    category: "Performance",
    min: -1,
  },
  {
    key: "region-file-compression",
    label: "Region Compression",
    type: "select",
    category: "Performance",
    options: ["deflate", "lz4", "none"],
  },
  {
    key: "broadcast-console-to-ops",
    label: "Broadcast Console to OPs",
    type: "boolean",
    category: "Admin",
  },
  {
    key: "broadcast-rcon-to-ops",
    label: "Broadcast RCON to OPs",
    type: "boolean",
    category: "Admin",
  },
  {
    key: "enable-jmx-monitoring",
    label: "JMX Monitoring",
    type: "boolean",
    category: "Admin",
  },
  {
    key: "log-ips",
    label: "Log IP Addresses",
    type: "boolean",
    category: "Admin",
  },
  {
    key: "bug-report-link",
    label: "Bug Report Link",
    type: "text",
    category: "Admin",
  },
  {
    key: "text-filtering-config",
    label: "Text Filtering Config",
    type: "text",
    category: "Advanced",
  },
  {
    key: "text-filtering-version",
    label: "Text Filtering Version",
    type: "number",
    category: "Advanced",
    min: 0,
  },
  {
    key: "initial-enabled-packs",
    label: "Initial Enabled Packs",
    type: "list",
    category: "Advanced",
    placeholder: "vanilla",
  },
  {
    key: "initial-disabled-packs",
    label: "Initial Disabled Packs",
    type: "list",
    category: "Advanced",
  },
];

const defaultServerProperties = `# Minecraft server properties
accepts-transfers=false
allow-flight=false
allow-nether=true
broadcast-console-to-ops=true
broadcast-rcon-to-ops=true
bug-report-link=
difficulty=easy
enable-command-block=false
enable-jmx-monitoring=false
enable-query=false
enable-rcon=false
enable-status=true
enforce-secure-profile=true
enforce-whitelist=false
entity-broadcast-range-percentage=100
force-gamemode=false
function-permission-level=2
gamemode=survival
generate-structures=true
generator-settings={}
hardcore=false
hide-online-players=false
initial-disabled-packs=
initial-enabled-packs=vanilla
level-name=world
level-seed=
level-type=minecraft:normal
log-ips=true
max-chained-neighbor-updates=1000000
max-players=20
max-tick-time=60000
max-world-size=29999984
motd=A Minecraft Server
network-compression-threshold=256
online-mode=true
op-permission-level=4
pause-when-empty-seconds=60
player-idle-timeout=0
prevent-proxy-connections=false
pvp=true
query.port=25565
rate-limit=0
rcon.password=
rcon.port=25575
region-file-compression=deflate
require-resource-pack=false
resource-pack=
resource-pack-id=
resource-pack-prompt=
resource-pack-sha1=
server-ip=
server-port=25565
simulation-distance=10
spawn-animals=true
spawn-monsters=true
spawn-npcs=true
spawn-protection=16
sync-chunk-writes=true
text-filtering-config=
text-filtering-version=0
use-native-transport=true
view-distance=10
white-list=false
`;

function parseProperties(content: string): PropertiesMap {
  const out: PropertiesMap = {};
  for (const line of content.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith("!"))
      continue;
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

type ResourcePackSettings = {
  enabled?: boolean;
  path?: string;
  public_id?: string;
  sha1?: string;
  file_name?: string;
  source?: "uploaded" | "world";
  required?: boolean;
  prompt?: string;
  updated_at?: string;
};

type WorldResourcePack = {
  path: string;
  worldName: string;
  size: number;
  modified: string;
};

const RESOURCE_PACK_DIR = "/resource-packs";

function getResourcePackSettings(server: Server): ResourcePackSettings {
  const value = server.settings?.resource_pack;
  if (!value || typeof value !== "object") return {};
  return value as ResourcePackSettings;
}

function makePublicId() {
  const browserCrypto = globalThis.crypto as Crypto & {
    randomUUID?: () => string;
  };
  if (browserCrypto.randomUUID) return browserCrypto.randomUUID();
  const bytes = new Uint8Array(16);
  browserCrypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

function resourcePackPathFor(fileName: string) {
  const cleaned = fileName
    .replace(/\\/g, "/")
    .split("/")
    .pop()
    ?.replace(/[^a-zA-Z0-9._-]/g, "-");
  const name =
    cleaned && cleaned.toLowerCase().endsWith(".zip")
      ? cleaned
      : "resource-pack.zip";
  return `${RESOURCE_PACK_DIR}/${name}`;
}

function resourcePackSourceFor(path: string): ResourcePackSettings["source"] {
  return path.toLowerCase().endsWith("/resources.zip") ? "world" : "uploaded";
}

async function sha1Hex(file: File) {
  const digest = await crypto.subtle.digest("SHA-1", await file.arrayBuffer());
  return Array.from(new Uint8Array(digest), (b) =>
    b.toString(16).padStart(2, "0"),
  ).join("");
}

async function sha1Bytes(bytes: Uint8Array) {
  const buffer = new ArrayBuffer(bytes.byteLength);
  new Uint8Array(buffer).set(bytes);
  const digest = await crypto.subtle.digest("SHA-1", buffer);
  return Array.from(new Uint8Array(digest), (b) =>
    b.toString(16).padStart(2, "0"),
  ).join("");
}

function getPropertyGroups(values: PropertiesMap) {
  const knownKeys = new Set(propertyFields.map((field) => field.key));
  const customFields: PropertyField[] = Object.keys(values)
    .filter((key) => !knownKeys.has(key))
    .sort((a, b) => a.localeCompare(b))
    .map((key) => ({
      key,
      label: key,
      type: "text",
      category: "Other",
    }));
  const allFields = [...propertyFields, ...customFields];

  return propertyCategories
    .map((category) => ({
      category,
      fields: allFields.filter((field) => field.category === category),
    }))
    .filter((group) => group.fields.length > 0);
}

function PropertyFieldControl({
  field,
  value,
  onChange,
}: {
  field: PropertyField;
  value: string;
  onChange: (value: string) => void;
}) {
  const inputClass =
    "flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent";

  if (field.type === "select") {
    return (
      <select
        className={inputClass}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">Default</option>
        {(field.options ?? []).map((option) => (
          <option key={option} value={option}>
            {option}
          </option>
        ))}
      </select>
    );
  }

  if (field.type === "boolean") {
    const enabled = value === "true";
    return (
      <button
        type="button"
        role="switch"
        aria-checked={enabled}
        onClick={() => onChange(enabled ? "false" : "true")}
        title={enabled ? "Disable" : "Enable"}
        className={`relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors ${
          enabled ? "bg-accent" : "border border-border-hover bg-background"
        }`}
      >
        <span
          className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${
            enabled ? "translate-x-[18px]" : "translate-x-0.5"
          }`}
        />
      </button>
    );
  }

  if (field.type === "textarea") {
    return (
      <textarea
        className="min-h-20 w-full resize-y rounded-md border border-border bg-surface-2 px-3 py-2 text-sm text-text-primary placeholder:text-text-secondary focus:outline-none focus:ring-2 focus:ring-accent"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={field.placeholder}
      />
    );
  }

  return (
    <Input
      type={field.type === "number" ? "number" : "text"}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      min={field.min}
      max={field.max}
      step={field.step}
      placeholder={field.placeholder}
      className={field.type === "list" ? "font-mono" : undefined}
    />
  );
}

function ServerPropertiesPanel({ serverId }: { serverId: string }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [values, setValues] = useState<PropertiesMap>({});
  const [saveBarPinned, setSaveBarPinned] = useState(false);
  const saveBarSentinelRef = useRef<HTMLDivElement>(null);

  const propsQuery = useQuery({
    queryKey: ["file-content", serverId, "/server.properties"],
    queryFn: () => api.files.readContent(serverId, "/server.properties"),
    retry: false,
  });

  const original = propsQuery.data ?? "";

  // Populate values during render (not in an effect) so the fields' first paint
  // already reflects the loaded data. Doing this in an effect would paint every
  // switch in its default "off" state for one frame, then flip the on-by-default
  // ones, making them animate on load.
  const [loadedFrom, setLoadedFrom] = useState<string | null>(null);
  if (propsQuery.data !== undefined && propsQuery.data !== loadedFrom) {
    setLoadedFrom(propsQuery.data);
    setValues(parseProperties(propsQuery.data));
  }

  useEffect(() => {
    const sentinel = saveBarSentinelRef.current;
    const scrollRoot = sentinel?.closest("[data-server-scroll]");
    if (!sentinel || !(scrollRoot instanceof HTMLElement)) return;

    const observer = new IntersectionObserver(
      ([entry]) => {
        const isPinned = entry.rootBounds
          ? entry.boundingClientRect.top < entry.rootBounds.top
          : !entry.isIntersecting;
        setSaveBarPinned(isPinned);
      },
      {
        root: scrollRoot,
        threshold: 1,
      },
    );

    observer.observe(sentinel);
    return () => observer.disconnect();
  }, []);

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
    <div>
      {/* Docked toolbar. Once the header below scrolls completely out of view,
          this full-width bar slides DOWN out from behind the page banner with
          a compact copy of the title + Save button. A zero-height sticky host
          keeps it pinned across the whole list without reserving layout; the
          bar is parked above the fold and clipped by the scroll container, so
          its top edge is flush with the banner for the entire slide. */}
      <div className="sticky top-0 z-30 h-0">
        <div
          className={`save-toolbar absolute -left-4 -right-4 top-0 flex items-center justify-between gap-4 border-b border-border bg-surface px-4 py-2.5 sm:-left-6 sm:-right-6 sm:px-6 ${
            saveBarPinned ? "save-toolbar--pinned" : "pointer-events-none"
          }`}
        >
          <div className="min-w-0">
            <h3 className="truncate text-sm font-semibold text-text-primary">
              Minecraft Properties
            </h3>
            <p className="truncate text-xs text-text-secondary">
              Edits /server.properties on the server.
            </p>
          </div>
          <Button
            size="sm"
            className="flex-shrink-0"
            onClick={() => saveMutation.mutate()}
            loading={saveMutation.isPending}
            disabled={propsQuery.isLoading || propsQuery.isError}
          >
            <Save className="h-3.5 w-3.5" /> Save Properties
          </Button>
        </div>
      </div>

      {/* Header — normal flow, scrolls away with the page. */}
      <div className="flex items-start justify-between gap-4">
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

      {/* Sentinel at the header's bottom edge: once it leaves the top of the
          scroll area, the header + its Save button are completely out of view
          — exactly the moment the docked toolbar drops down. */}
      <div ref={saveBarSentinelRef} className="h-px" aria-hidden="true" />

      <div className="mt-4">
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
        <div className="space-y-6">
          {getPropertyGroups(values).map((group) => (
            <section key={group.category} className="space-y-3">
              <h4 className="text-xs font-semibold uppercase tracking-wide text-text-secondary">
                {group.category}
              </h4>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                {group.fields.map((field) => (
                  field.type === "boolean" ? (
                    <div
                      key={field.key}
                      className="flex h-9 items-center justify-between gap-3 rounded-md border border-border bg-surface-2 px-3"
                    >
                      <Label title={field.key} className="min-w-0 truncate">
                        {field.label}
                      </Label>
                      <PropertyFieldControl
                        field={field}
                        value={values[field.key] ?? ""}
                        onChange={(value) => setProp(field.key, value)}
                      />
                    </div>
                  ) : (
                    <div
                      key={field.key}
                      className={
                        field.type === "textarea"
                          ? "space-y-1.5 sm:col-span-2"
                          : "space-y-1.5"
                      }
                    >
                      <Label title={field.key} className="block">
                        {field.label}
                      </Label>
                      <PropertyFieldControl
                        field={field}
                        value={values[field.key] ?? ""}
                        onChange={(value) => setProp(field.key, value)}
                      />
                    </div>
                  )
                ))}
              </div>
            </section>
          ))}
        </div>
        )}
      </div>
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

function SoftwareOptionsPanel({ server }: { server: Server }) {
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
      success("Runtime settings saved");
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
    <Panel
        title="Runtime"
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
  );
}

function ResourcePackOptionsPanel({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const autoWorldDefaultRef = useRef<string | null>(null);
  const initial = getResourcePackSettings(server);
  const [form, setForm] = useState({
    enabled: initial.enabled ?? false,
    path: initial.path ?? "",
    public_id: initial.public_id ?? makePublicId(),
    sha1: initial.sha1 ?? "",
    file_name: initial.file_name ?? "",
    source: initial.source ?? resourcePackSourceFor(initial.path ?? ""),
    required: initial.required ?? false,
    prompt: initial.prompt ?? "",
  });

  useEffect(() => {
    const settings = getResourcePackSettings(server);
    setForm({
      enabled: settings.enabled ?? false,
      path: settings.path ?? "",
      public_id: settings.public_id ?? makePublicId(),
      sha1: settings.sha1 ?? "",
      file_name: settings.file_name ?? "",
      source: settings.source ?? resourcePackSourceFor(settings.path ?? ""),
      required: settings.required ?? false,
      prompt: settings.prompt ?? "",
    });
  }, [server]);

  const filesQuery = useQuery({
    queryKey: ["files", server.id, RESOURCE_PACK_DIR],
    queryFn: () => api.files.list(server.id, RESOURCE_PACK_DIR),
    retry: false,
  });

  const worldPacksQuery = useQuery({
    queryKey: ["world-resource-packs", server.id],
    queryFn: async () => {
      const root = await api.files.list(server.id, "/");
      const dirs = root.entries.filter(
        (entry) =>
          entry.type === "dir" &&
          entry.name.toLowerCase() !== RESOURCE_PACK_DIR.slice(1),
      );
      const packs: WorldResourcePack[] = [];

      await Promise.all(
        dirs.map(async (dir) => {
          const worldPath = `/${dir.name}`;
          const listing = await api.files.list(server.id, worldPath).catch(() => null);
          const pack = listing?.entries.find(
            (entry) =>
              entry.type === "file" &&
              entry.name.toLowerCase() === "resources.zip",
          );
          if (pack) {
            packs.push({
              path: `${worldPath}/resources.zip`,
              worldName: dir.name,
              size: pack.size,
              modified: pack.modified,
            });
          }
        }),
      );

      return packs.sort((a, b) => a.worldName.localeCompare(b.worldName));
    },
    retry: false,
  });

  const zipFiles =
    filesQuery.data?.entries.filter(
      (entry) => entry.type === "file" && entry.name.toLowerCase().endsWith(".zip"),
    ) ?? [];
  const worldPacks = worldPacksQuery.data ?? [];

  useEffect(() => {
    const settings = getResourcePackSettings(server);
    const detected = worldPacks[0];
    if (
      settings.path ||
      !detected ||
      autoWorldDefaultRef.current === server.id
    ) {
      return;
    }

    autoWorldDefaultRef.current = server.id;
    setForm((prev) => {
      if (prev.path) return prev;
      return {
        ...prev,
        enabled: true,
        path: detected.path,
        sha1: "",
        file_name: "resources.zip",
        source: "world",
        required: true,
      };
    });
  }, [server, worldPacks]);

  const publicUrl = form.public_id
    ? api.resourcePacks.publicUrl(server.id, form.public_id)
    : "";

  const saveResourcePack = async (nextForm: typeof form) => {
    const publicId = nextForm.public_id || makePublicId();
    if (nextForm.enabled && !nextForm.path) {
      throw new Error("Choose or upload a resource pack zip first.");
    }
    const computedSHA1 = nextForm.enabled
      ? nextForm.sha1.trim() ||
        (await api.files
          .readBytes(server.id, nextForm.path)
          .then((bytes) => sha1Bytes(bytes)))
      : "";

    const advertisedUrl = nextForm.enabled
      ? api.resourcePacks.publicUrl(server.id, publicId)
      : "";
    const properties = await api.files
      .readContent(server.id, "/server.properties")
      .catch(() => defaultServerProperties);
    const values = {
      ...parseProperties(properties),
      "resource-pack": advertisedUrl,
      "resource-pack-id": "",
      "resource-pack-sha1": nextForm.enabled ? computedSHA1 : "",
      "require-resource-pack": String(nextForm.enabled && nextForm.required),
      "resource-pack-prompt": nextForm.enabled ? nextForm.prompt : "",
    };

    await api.files.writeContent(
      server.id,
      "/server.properties",
      serializeProperties(properties, values),
    );
    await api.servers.update(server.id, {
      settings: {
        ...server.settings,
        resource_pack: {
          enabled: nextForm.enabled,
          path: nextForm.path,
          public_id: publicId,
          sha1: computedSHA1,
          file_name: nextForm.file_name,
          source: nextForm.source,
          required: nextForm.required,
          prompt: nextForm.prompt,
          updated_at: new Date().toISOString(),
        },
      },
    });
    return { ...nextForm, public_id: publicId, sha1: computedSHA1 };
  };

  const saveMutation = useMutation({
    mutationFn: () => saveResourcePack(form),
    onSuccess: (saved) => {
      setForm(saved);
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      qc.invalidateQueries({
        queryKey: ["file-content", server.id, "/server.properties"],
      });
      success(
        "Resource pack settings saved",
        server.status === "online"
          ? "Restart the Minecraft server to make players receive the new pack."
          : undefined,
      );
    },
    onError: (e: Error) => error("Save failed", e.message),
  });

  const uploadMutation = useMutation({
    mutationFn: async (file: File) => {
      if (!file.name.toLowerCase().endsWith(".zip")) {
        throw new Error("Resource packs must be .zip files.");
      }
      const path = resourcePackPathFor(file.name);
      const storedName = path.split("/").pop() ?? "resource-pack.zip";
      const uploadFile = new File([file], storedName, {
        type: file.type || "application/zip",
        lastModified: file.lastModified,
      });
      const sha1 = await sha1Hex(file);
      await api.files.upload(server.id, RESOURCE_PACK_DIR, uploadFile);
      const next = {
        ...form,
        enabled: true,
        path,
        public_id: form.public_id || makePublicId(),
        sha1,
        file_name: storedName,
        source: "uploaded" as const,
      };
      return saveResourcePack(next);
    },
    onSuccess: (saved) => {
      setForm(saved);
      qc.invalidateQueries({ queryKey: ["files", server.id, RESOURCE_PACK_DIR] });
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({
        queryKey: ["file-content", server.id, "/server.properties"],
      });
      success(
        "Resource pack uploaded",
        server.status === "online"
          ? "Restart the Minecraft server to make players receive the new pack."
          : undefined,
      );
    },
    onError: (e: Error) => error("Upload failed", e.message),
  });

  const copyUrl = async () => {
    await navigator.clipboard.writeText(publicUrl);
    success("Resource pack URL copied");
  };

  return (
    <Panel
      title="Resource Pack"
      description="Host one zip for this server and write its URL into server.properties."
      actions={
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => fileInputRef.current?.click()}
            loading={uploadMutation.isPending}
          >
            {!uploadMutation.isPending && <Upload className="h-3.5 w-3.5" />}
            Upload
          </Button>
          <Button
            size="sm"
            onClick={() => saveMutation.mutate()}
            loading={saveMutation.isPending}
          >
            {!saveMutation.isPending && <Save className="h-3.5 w-3.5" />}
            Save
          </Button>
        </div>
      }
    >
      <input
        ref={fileInputRef}
        type="file"
        accept=".zip,application/zip"
        className="hidden"
        onChange={(e) => {
          const file = e.target.files?.[0];
          e.target.value = "";
          if (file) uploadMutation.mutate(file);
        }}
      />

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <div className="space-y-4">
          <label className="flex items-center gap-2 text-sm text-text-primary">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) =>
                setForm((p) => ({ ...p, enabled: e.target.checked }))
              }
            />
            Enabled
          </label>

          <div className="space-y-1.5">
            <Label>Hosted Zip</Label>
            <select
              className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
              value={form.path}
              onChange={(e) => {
                const path = e.target.value;
                const worldPack = worldPacks.find((pack) => pack.path === path);
                setForm((p) => ({
                  ...p,
                  path,
                  sha1: path === p.path ? p.sha1 : "",
                  file_name: path.split("/").pop() ?? p.file_name,
                  source: resourcePackSourceFor(path),
                  enabled: worldPack ? true : p.enabled,
                  required: worldPack ? true : p.required,
                }));
              }}
            >
              {form.path && <option value={form.path}>{form.path}</option>}
              <option value="">No hosted pack</option>
              {worldPacks.length > 0 && (
                <optgroup label="World resources.zip">
                  {worldPacks.map((pack) => (
                    <option key={pack.path} value={pack.path}>
                      {pack.worldName}/resources.zip
                    </option>
                  ))}
                </optgroup>
              )}
              {zipFiles.map((entry) => {
                const path = `${RESOURCE_PACK_DIR}/${entry.name}`;
                return (
                  <option key={path} value={path}>
                    {entry.name}
                  </option>
                );
              })}
            </select>
            {filesQuery.isError && (
              <p className="text-xs text-text-secondary">
                Upload a zip to create the resource-packs folder.
              </p>
            )}
          </div>

          <div className="space-y-1.5">
            <Label>SHA-1</Label>
            <Input
              value={form.sha1}
              onChange={(e) => setForm((p) => ({ ...p, sha1: e.target.value }))}
              className="font-mono"
              placeholder="Computed automatically on upload"
            />
          </div>

          <label className="flex items-center gap-2 text-sm text-text-primary">
            <input
              type="checkbox"
              checked={form.required}
              onChange={(e) =>
                setForm((p) => ({ ...p, required: e.target.checked }))
              }
            />
            Require pack
          </label>

          <div className="space-y-1.5">
            <Label>Prompt</Label>
            <Input
              value={form.prompt}
              onChange={(e) =>
                setForm((p) => ({ ...p, prompt: e.target.value }))
              }
              placeholder="Optional message shown to players"
            />
          </div>
        </div>

        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label>Public URL</Label>
            <div className="flex gap-2">
              <Input
                value={publicUrl}
                readOnly
                className="min-w-0 flex-1 font-mono"
              />
              <Button
                variant="outline"
                size="icon"
                onClick={copyUrl}
                title="Copy URL"
                disabled={!publicUrl}
              >
                <Copy className="h-4 w-4" />
              </Button>
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>Public ID</Label>
            <div className="flex gap-2">
              <Input
                value={form.public_id}
                onChange={(e) =>
                  setForm((p) => ({ ...p, public_id: e.target.value }))
                }
                className="min-w-0 flex-1 font-mono"
              />
              <Button
                variant="outline"
                size="icon"
                onClick={() =>
                  setForm((p) => ({ ...p, public_id: makePublicId() }))
                }
                title="Regenerate public ID"
              >
                <Link2 className="h-4 w-4" />
              </Button>
            </div>
          </div>
          <p className="text-xs leading-5 text-text-secondary">
            Saving writes resource-pack, resource-pack-sha1,
            require-resource-pack, and resource-pack-prompt in
            /server.properties.
          </p>
        </div>
      </div>
    </Panel>
  );
}

function PanelOptionsPanel({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const navigate = useNavigate();
  const [showDelete, setShowDelete] = useState(false);
  const [purgeFiles, setPurgeFiles] = useState(false);
  const [purgeBackups, setPurgeBackups] = useState(false);

  const [form, setForm] = useState({
    name: server.name,
    description: server.description ?? "",
    directory_path: server.directory_path,
    port: String(server.port),
    ram_mb_max: String(server.ram_mb_max),
    ram_mb_min: String(server.ram_mb_min),
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
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      success("Settings saved");
    },
    onError: (e: Error) => error("Save failed", e.message),
  });

  const deleteMutation = useMutation({
    mutationFn: () =>
      api.servers.delete(server.id, {
        files: purgeFiles,
        backups: purgeBackups,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      navigate({ to: "/servers" });
    },
    onError: (e: Error) => error("Delete failed", e.message),
  });

  const openDelete = () => {
    setPurgeFiles(false);
    setPurgeBackups(false);
    setShowDelete(true);
  };

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }));

  return (
    <>
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
          Deleting a server removes it from the panel. By default the files on
          the node are kept — tick the options below to also wipe them from disk.
        </p>
        <Button variant="destructive" size="sm" onClick={openDelete}>
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
      >
        <div className="mt-4 space-y-2 rounded-md border border-border bg-surface p-3">
          <label className="flex items-center gap-2 text-sm text-text-secondary cursor-pointer">
            <input
              type="checkbox"
              checked={purgeFiles}
              onChange={(e) => setPurgeFiles(e.target.checked)}
            />
            Also delete the server files on disk
          </label>
          <label className="flex items-center gap-2 text-sm text-text-secondary cursor-pointer">
            <input
              type="checkbox"
              checked={purgeBackups}
              onChange={(e) => setPurgeBackups(e.target.checked)}
            />
            Also delete all backups
          </label>
          {(purgeFiles || purgeBackups) && (
            <p className="text-xs text-red-400">
              Permanently erases the selected data from the node. The node must
              be online or the delete will be cancelled.
            </p>
          )}
        </div>
      </ConfirmDialog>
    </>
  );
}

function OptionsTab({ server }: { server: Server }) {
  return (
    <div className="max-w-3xl space-y-5">
      <SoftwareOptionsPanel server={server} />
      <ResourcePackOptionsPanel server={server} />
      <PanelOptionsPanel server={server} />
    </div>
  );
}

function PropertiesTab({ serverId }: { serverId: string }) {
  return (
    <div className="max-w-5xl pt-4 sm:pt-6">
      <ServerPropertiesPanel serverId={serverId} />
    </div>
  );
}

type LogFileItem = {
  path: string;
  name: string;
  kind: "log" | "crash";
  size: number;
  modified: string;
};

function formatLogFileSize(bytes: number) {
  if (bytes <= 0) return "-";
  if (bytes >= 1_048_576) return `${(bytes / 1_048_576).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${bytes} B`;
}

function logFileName(path: string) {
  return path.split("/").filter(Boolean).pop() ?? path;
}

async function readGzipLog(serverId: string, path: string) {
  if (!("DecompressionStream" in window)) {
    throw new Error("Gzip extraction is not supported in this browser.");
  }

  const bytes = await api.files.readBytes(serverId, path);
  const stream = new Blob([bytes as BlobPart])
    .stream()
    .pipeThrough(new DecompressionStream("gzip"));
  return new Response(stream).text();
}

function LogsTab({ serverId }: { serverId: string }) {
  const [path, setPath] = useState("/logs/latest.log");
  const [search, setSearch] = useState("");
  const [extractedLog, setExtractedLog] = useState<{
    path: string;
    content: string;
  } | null>(null);
  const [extractingLog, setExtractingLog] = useState(false);
  const [extractError, setExtractError] = useState<string | null>(null);
  const {
    data: logFiles = [],
    isLoading: isLoadingFiles,
    refetch: refetchFiles,
  } = useQuery({
    queryKey: ["log-files", serverId],
    queryFn: async () => {
      const roots: Array<{ path: string; kind: LogFileItem["kind"] }> = [
        { path: "/logs", kind: "log" },
        { path: "/crash-reports", kind: "crash" },
      ];

      const groups = await Promise.all(
        roots.map(async (root) => {
          try {
            const tree = await api.files.tree(serverId, root.path);
            return tree.entries
              .filter((entry) => entry.type === "file")
              .map((entry) => {
                const fullPath = `${root.path}/${entry.path}`.replace(
                  /\/+/g,
                  "/",
                );
                return {
                  path: fullPath,
                  name: logFileName(fullPath),
                  kind: root.kind,
                  size: entry.size,
                  modified: entry.modified,
                };
              });
          } catch {
            return [];
          }
        }),
      );

      return groups
        .flat()
        .sort(
          (a, b) =>
            new Date(b.modified).getTime() - new Date(a.modified).getTime(),
      );
    },
  });

  useEffect(() => {
    setExtractedLog(null);
    setExtractError(null);
  }, [path]);

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ["file-content", serverId, path],
    queryFn: () => api.files.readContent(serverId, path),
    enabled: path.trim().length > 0 && !path.toLowerCase().endsWith(".gz"),
    retry: false,
  });

  const isCompressedLog = path.trim().toLowerCase().endsWith(".gz");
  const visibleLog =
    isCompressedLog && extractedLog?.path === path ? extractedLog.content : data;

  const filteredLogFiles = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) return logFiles;
    return logFiles.filter(
      (file) =>
        file.path.toLowerCase().includes(query) ||
        file.name.toLowerCase().includes(query) ||
        file.kind.includes(query),
    );
  }, [logFiles, search]);

  const selectedLogFile = logFiles.find((file) => file.path === path);

  const refreshAll = () => {
    refetchFiles();
    if (isCompressedLog) {
      setExtractedLog(null);
      setExtractError(null);
    } else {
      refetch();
    }
  };

  const extractCompressedLog = async () => {
    const currentPath = path.trim();
    if (!currentPath) return;

    setExtractingLog(true);
    setExtractError(null);
    try {
      const content = await readGzipLog(serverId, currentPath);
      setExtractedLog({ path: currentPath, content });
    } catch (err) {
      setExtractedLog(null);
      setExtractError(err instanceof Error ? err.message : "Failed to extract log.");
    } finally {
      setExtractingLog(false);
    }
  };

  return (
    <div className="grid h-full min-h-0 gap-4 p-4 sm:p-5 lg:grid-cols-[20rem_minmax(0,1fr)]">
      <div className="flex min-h-0 flex-col rounded-md border border-border bg-surface">
        <div className="border-b border-border p-3">
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-secondary" />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search logs and crashes"
              className="pl-9"
            />
          </div>
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          {isLoadingFiles ? (
            <div className="flex justify-center py-8">
              <div className="h-5 w-5 animate-spin rounded-full border-2 border-accent border-t-transparent" />
            </div>
          ) : filteredLogFiles.length === 0 ? (
            <div className="px-4 py-8 text-center text-sm text-text-secondary">
              {logFiles.length === 0
                ? "No logs or crash reports found."
                : "No matching logs or crash reports."}
            </div>
          ) : (
            <div className="divide-y divide-border">
              {filteredLogFiles.map((file) => {
                const active = file.path === path;
                return (
                  <button
                    key={file.path}
                    type="button"
                    onClick={() => setPath(file.path)}
                    className={[
                      "flex w-full items-start gap-3 px-3 py-3 text-left transition-colors",
                      active ? "bg-accent/10" : "hover:bg-surface-2",
                    ].join(" ")}
                  >
                    {file.kind === "crash" ? (
                      <FileWarning className="mt-0.5 h-4 w-4 shrink-0 text-red-400" />
                    ) : file.path.toLowerCase().endsWith(".gz") ? (
                      <FileArchive className="mt-0.5 h-4 w-4 shrink-0 text-text-secondary" />
                    ) : (
                      <FileText className="mt-0.5 h-4 w-4 shrink-0 text-text-secondary" />
                    )}
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium text-text-primary">
                        {file.name}
                      </span>
                      <span className="mt-1 block truncate text-xs text-text-secondary">
                        {file.path}
                      </span>
                      <span className="mt-1 flex items-center gap-2 text-xs text-text-secondary">
                        <Badge
                          variant={file.kind === "crash" ? "error" : "muted"}
                        >
                          {file.kind === "crash" ? "crash" : "log"}
                        </Badge>
                        <span>{formatLogFileSize(file.size)}</span>
                        <span>{new Date(file.modified).toLocaleString()}</span>
                      </span>
                    </span>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      </div>

      <div className="flex min-h-0 flex-col">
        <div className="mb-3 flex flex-col gap-2 sm:flex-row sm:items-center">
          <Input
            value={path}
            onChange={(e) => setPath(e.target.value)}
            className="min-w-0 flex-1 font-mono"
          />
          <Button
            variant="outline"
            onClick={refreshAll}
            className="shrink-0"
          >
            <RefreshCw className="h-4 w-4" />
            Refresh
          </Button>
          {isCompressedLog && (
            <Button
              onClick={extractCompressedLog}
              disabled={extractingLog || !path.trim()}
              className="shrink-0"
            >
              <FileArchive className="h-4 w-4" />
              {extractingLog ? "Extracting..." : "Extract view"}
            </Button>
          )}
        </div>
        {selectedLogFile && (
          <div className="mb-3 flex flex-wrap items-center gap-2 text-xs text-text-secondary">
            <Badge variant={selectedLogFile.kind === "crash" ? "error" : "muted"}>
              {selectedLogFile.kind === "crash" ? "crash report" : "log file"}
            </Badge>
            <span>{formatLogFileSize(selectedLogFile.size)}</span>
            <span>
              Modified {new Date(selectedLogFile.modified).toLocaleString()}
            </span>
          </div>
        )}
        <div className="min-h-0 flex-1 overflow-auto rounded-md border border-border bg-[#0f0f0f] p-4 font-mono text-xs leading-5 text-text-primary">
          {isCompressedLog && extractError ? (
            <div className="text-red-400">{extractError}</div>
          ) : isCompressedLog && extractedLog?.path !== path ? (
            <div className="text-text-secondary">
              This log is compressed. Use Extract view to temporarily decompress
              it here.
            </div>
          ) : isLoading ? (
            <div className="text-text-secondary">Loading log...</div>
          ) : isError ? (
            <div className="text-text-secondary">
              No log file found at {path}.
            </div>
          ) : (
            <pre className="whitespace-pre-wrap">{visibleLog}</pre>
          )}
        </div>
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
