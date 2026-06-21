import { useEffect, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, ChevronDown, Pencil, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Dialog, ConfirmDialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import {
  buildCron,
  describeAction,
  describeCron,
  DEFAULT_SCHEDULE,
  parseCron,
  TASK_ACTIONS,
  WEEKDAY_OPTIONS,
  type ScheduleKind,
  type ScheduleState,
} from "@/lib/cron";
import type { ScheduledTask } from "@/lib/types";

const EMPTY_TASK_FORM = {
  name: "",
  action: "command",
  command: "",
};

const FREQUENCIES: { value: ScheduleKind; label: string }[] = [
  { value: "hourly", label: "Hourly" },
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
  { value: "monthly", label: "Monthly" },
  { value: "custom", label: "Custom (cron)" },
];

const selectClass =
  "flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent";

// Build a "HH:MM" value for the native time input and back, in local terms of
// the schedule's hour/minute fields.
function toTimeValue(hour: number, minute: number): string {
  return `${String(hour).padStart(2, "0")}:${String(minute).padStart(2, "0")}`;
}

function ScheduleBuilder({
  value,
  onChange,
}: {
  value: ScheduleState;
  onChange: (next: ScheduleState) => void;
}) {
  const [advanced, setAdvanced] = useState(value.kind === "custom");
  const set = (patch: Partial<ScheduleState>) => {
    const next = { ...value, ...patch };
    // Keep raw in sync with the builder so the advanced field always mirrors.
    onChange(
      next.kind === "custom" ? next : { ...next, raw: buildCron(next) },
    );
  };

  const onTime = (e: React.ChangeEvent<HTMLInputElement>) => {
    const [h, m] = e.target.value.split(":").map(Number);
    set({ hour: h || 0, minute: m || 0 });
  };

  return (
    <div className="space-y-3">
      <div className="space-y-1.5">
        <Label>Frequency</Label>
        <select
          className={selectClass}
          value={value.kind}
          onChange={(e) => {
            const kind = e.target.value as ScheduleKind;
            setAdvanced(kind === "custom");
            set({ kind });
          }}
        >
          {FREQUENCIES.map((f) => (
            <option key={f.value} value={f.value}>
              {f.label}
            </option>
          ))}
        </select>
      </div>

      {value.kind === "hourly" && (
        <div className="space-y-1.5">
          <Label>At minute</Label>
          <Input
            type="number"
            min={0}
            max={59}
            value={value.minute}
            onChange={(e) => set({ minute: Number(e.target.value) })}
          />
        </div>
      )}

      {value.kind === "weekly" && (
        <div className="space-y-1.5">
          <Label>Day of week</Label>
          <select
            className={selectClass}
            value={value.weekday}
            onChange={(e) => set({ weekday: Number(e.target.value) })}
          >
            {WEEKDAY_OPTIONS.map((d) => (
              <option key={d.value} value={d.value}>
                {d.label}
              </option>
            ))}
          </select>
        </div>
      )}

      {value.kind === "monthly" && (
        <div className="space-y-1.5">
          <Label>Day of month</Label>
          <Input
            type="number"
            min={1}
            max={31}
            value={value.day}
            onChange={(e) => set({ day: Number(e.target.value) })}
          />
        </div>
      )}

      {(value.kind === "daily" ||
        value.kind === "weekly" ||
        value.kind === "monthly") && (
        <div className="space-y-1.5">
          <Label>Time of day</Label>
          <Input
            type="time"
            value={toTimeValue(value.hour, value.minute)}
            onChange={onTime}
          />
        </div>
      )}

      {value.kind !== "custom" && (
        <button
          type="button"
          onClick={() => setAdvanced((v) => !v)}
          className="flex items-center gap-1 text-xs text-text-secondary hover:text-text-primary"
        >
          <ChevronDown
            className={`h-3.5 w-3.5 transition-transform ${advanced ? "" : "-rotate-90"}`}
          />
          Advanced (raw cron)
        </button>
      )}

      {(advanced || value.kind === "custom") && (
        <div className="space-y-1.5">
          {value.kind !== "custom" && <Label>Cron expression</Label>}
          <Input
            value={value.raw}
            onChange={(e) =>
              onChange({ ...parseCron(e.target.value), raw: e.target.value })
            }
            className="font-mono"
            placeholder="0 4 * * *"
          />
          <p className="text-xs text-text-secondary">
            Format: minute hour day month weekday
          </p>
        </div>
      )}

      <p className="rounded-md border border-border bg-surface-2 px-3 py-2 text-xs text-text-secondary">
        Runs{" "}
        <span className="text-text-primary">{describeCron(value.raw)}</span>
      </p>
    </div>
  );
}

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
  const [schedule, setSchedule] = useState<ScheduleState>(DEFAULT_SCHEDULE);

  // Load the task being edited (or reset) whenever the dialog opens.
  useEffect(() => {
    if (!open) return;
    setForm(
      task
        ? {
            name: task.name,
            action: task.action,
            command: (task.payload?.command as string) ?? "",
          }
        : EMPTY_TASK_FORM,
    );
    setSchedule(task ? parseCron(task.cron_expr) : DEFAULT_SCHEDULE);
  }, [open, task]);

  const mutation = useMutation({
    mutationFn: () => {
      const data = {
        name: form.name,
        cron_expr: buildCron(schedule),
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
        <ScheduleBuilder value={schedule} onChange={setSchedule} />
        <div className="space-y-1.5">
          <Label>Action</Label>
          <select className={selectClass} value={form.action} onChange={f("action")}>
            {TASK_ACTIONS.map((a) => (
              <option key={a.value} value={a.value}>
                {a.label}
              </option>
            ))}
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
          <dd className="text-text-primary">{describeAction(task.action)}</dd>
          {command && (
            <>
              <dt className="text-text-secondary">Command</dt>
              <dd className="break-all font-mono text-text-primary">
                {command}
              </dd>
            </>
          )}
          <dt className="text-text-secondary">Schedule</dt>
          <dd className="text-text-primary">
            {describeCron(task.cron_expr)}
            <span className="ml-2 font-mono text-xs text-text-secondary">
              {task.cron_expr}
            </span>
          </dd>
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
              className={`flex items-center gap-3 px-4 py-3 ${i < tasks.length - 1 ? "border-b border-border/50" : ""}`}
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
                <p className="truncate text-sm font-medium text-text-primary hover:text-accent">
                  {task.name}
                </p>
                <p className="truncate text-xs text-text-secondary mt-0.5">
                  {describeCron(task.cron_expr)} ·{" "}
                  {describeAction(task.action, task.payload)}
                </p>
              </button>
              {/* The verbose next/last timestamps eat the row's width. Inside the
                  server view two sidebars already claim ~448px, so the content
                  pane stays cramped until the viewport is wide — only surface
                  these at xl (still shown in the details dialog otherwise). The
                  name/schedule above truncate so they can never overlap this. */}
              <div className="hidden text-right text-xs text-text-secondary flex-shrink-0 xl:block">
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
