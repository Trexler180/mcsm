import { useState } from "react";
import { ArrowRight, ShieldCheck } from "lucide-react";
import { Dialog } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import type { ModUpdate } from "@/lib/types";

/**
 * Guided confirmation for the safe-update flow. It previews exactly which mods
 * will change, explains the apply → restart → verify → auto-revert guarantee,
 * and nudges the operator to take a backup first.
 */
export function SafeUpdateDialog({
  open,
  updates,
  onClose,
  onConfirm,
}: {
  open: boolean;
  updates: ModUpdate[];
  onClose: () => void;
  onConfirm: (backupFirst: boolean) => void;
}) {
  const [backupFirst, setBackupFirst] = useState(true);

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Safe update"
      description={`${updates.length} mod${updates.length === 1 ? "" : "s"} will be updated`}
      titleIcon={<ShieldCheck className="h-5 w-5 text-accent" />}
      className="max-w-lg"
    >
      <div className="rounded-md border border-border bg-surface-2 p-3 text-sm text-text-secondary">
        Updates are applied, then the server is restarted and watched as it
        boots. Any update that breaks the boot is automatically reverted and
        blocklisted, so a bad release can't leave the server down.
      </div>

      {updates.length > 0 && (
        <div className="mt-4 max-h-56 divide-y divide-border overflow-y-auto rounded-md border border-border">
          {updates.map((u) => (
            <div
              key={u.mod_id}
              className="flex items-center gap-2 px-3 py-2 text-sm"
            >
              <span className="min-w-0 flex-1 truncate text-text-primary">
                {u.name}
              </span>
              <span className="flex items-center gap-1.5 font-mono text-xs text-text-secondary">
                <span className="truncate">{u.current_version}</span>
                <ArrowRight className="h-3 w-3 flex-shrink-0" />
                <span className="truncate text-green-400">
                  {u.latest_version}
                </span>
              </span>
            </div>
          ))}
        </div>
      )}

      <label className="mt-4 flex cursor-pointer items-start gap-3 rounded-md border border-border bg-surface-2 px-3 py-2.5">
        <input
          type="checkbox"
          className="mt-0.5"
          checked={backupFirst}
          onChange={(e) => setBackupFirst(e.target.checked)}
        />
        <div>
          <p className="text-sm font-medium text-text-primary">
            Create a backup first
          </p>
          <p className="text-xs text-text-secondary">
            Recommended. A backup is started before any files change so you can
            roll back the whole server if needed.
          </p>
        </div>
      </label>

      <div className="mt-5 flex justify-end gap-3">
        <Button variant="outline" onClick={onClose}>
          Cancel
        </Button>
        <Button onClick={() => onConfirm(backupFirst)} disabled={updates.length === 0}>
          <ShieldCheck className="h-4 w-4" /> Start safe update
        </Button>
      </div>
    </Dialog>
  );
}
