import { useEffect, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Pencil, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Dialog, ConfirmDialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { ScheduledTask } from "@/lib/types";

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

export function TasksTab({ serverId }: { serverId: string }) {
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
        <EmptyState
          icon={CalendarClock}
          title="No scheduled tasks"
          hint="Automate recurring backups, restarts, or console commands on a cron schedule."
          action={
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="h-3.5 w-3.5" /> New Task
            </Button>
          }
        />
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
