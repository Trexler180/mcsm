import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { HardDrive, RotateCcw, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { Backup } from "@/lib/types";

export function BackupsTab({ serverId }: { serverId: string }) {
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
        <EmptyState
          icon={HardDrive}
          title="No backups yet"
          hint="Create a backup before risky changes like mod updates, or schedule recurring backups under Tasks."
        />
      ) : (
        <div className="border border-border rounded-lg overflow-hidden">
          {backups.map((b, i) => (
            <div
              key={b.id}
              className={`flex items-center justify-between gap-3 px-4 py-3 ${i < backups.length - 1 ? "border-b border-border/50" : ""}`}
            >
              <div className="min-w-0">
                <p className={`text-sm font-medium ${statusColor[b.status]}`}>
                  {b.status.charAt(0).toUpperCase() + b.status.slice(1)}
                </p>
                <p className="text-xs text-text-secondary mt-0.5 truncate">
                  {new Date(b.started_at).toLocaleString()}
                  {b.trigger !== "manual" && ` · ${b.trigger}`}
                </p>
              </div>
              {/* Keep the meta + actions together and never let them shrink so the
                  Restore/Delete buttons stay tappable; the timestamp truncates. */}
              <div className="flex flex-shrink-0 items-center gap-2 sm:gap-3">
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
