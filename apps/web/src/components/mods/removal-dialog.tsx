import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  AlertTriangle,
  Archive,
  Ban,
  Loader2,
  Package,
  Trash2,
  Unlink,
} from "lucide-react";
import { Dialog } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { InstalledMod } from "@/lib/types";
import { sourceBadgeClass } from "./shared";

export type RemovalMode = "uninstall" | "disable";

/**
 * Confirmation for taking content off a server, which turns into a dependency
 * warning when other installed content needs it. Removing a library that four
 * mods depend on is the classic way to break a working server — the jar goes,
 * and the next boot fails on four missing dependencies — so the dialog answers
 * "what else does this touch?" before anything changes, and offers to take the
 * dependents offline in the same step rather than leaving a half-broken set.
 *
 * Disabling goes through the same check: as far as the loader is concerned, a
 * renamed jar and a deleted one are the same thing.
 */
export function ModRemovalDialog({
  open,
  serverId,
  mod,
  mode,
  pending,
  onClose,
  onConfirm,
}: {
  open: boolean;
  serverId: string;
  mod: InstalledMod | null;
  mode: RemovalMode;
  pending: boolean;
  onClose: () => void;
  onConfirm: (opts: { disableDependents: boolean }) => void;
}) {
  const { success, error } = useNotifications();
  const [disableDependents, setDisableDependents] = useState(true);
  const [backupPending, setBackupPending] = useState(false);
  const [backupStarted, setBackupStarted] = useState(false);

  // Fresh dialog, fresh choices — a leftover "backup started" from the last mod
  // would be a lie about this one.
  useEffect(() => {
    if (open) {
      setDisableDependents(true);
      setBackupStarted(false);
    }
  }, [open, mod?.id]);

  const {
    data: impact,
    isLoading,
    isError,
  } = useQuery({
    queryKey: ["mod-dependents", serverId, mod?.id],
    queryFn: () => api.mods.dependents(serverId, mod!.id),
    enabled: open && !!mod,
    // Always re-check on open: the answer decides whether files get deleted.
    staleTime: 0,
    gcTime: 0,
  });

  const required = impact?.required ?? [];
  const optional = impact?.optional ?? [];
  const orphaning = impact?.orphaning ?? [];
  const breaking = required.length > 0;
  const verb = mode === "uninstall" ? "Uninstall" : "Disable";
  const verbing = mode === "uninstall" ? "Removing" : "Disabling";

  const startBackup = () => {
    setBackupPending(true);
    api.backups
      .create(serverId)
      .then(() => {
        setBackupStarted(true);
        success("Backup started", "It runs in the background");
      })
      .catch((e: Error) => error("Backup failed to start", e.message))
      .finally(() => setBackupPending(false));
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={breaking ? "Dependency warning" : `${verb} content`}
      titleIcon={
        breaking ? (
          <AlertTriangle className="h-5 w-5 text-red-400" />
        ) : mode === "uninstall" ? (
          <Trash2 className="h-5 w-5 text-text-secondary" />
        ) : (
          <Ban className="h-5 w-5 text-text-secondary" />
        )
      }
      className="max-w-lg"
    >
      {isLoading ? (
        <div className="flex items-center gap-2 rounded-md border border-border bg-surface-2 px-3 py-4 text-sm text-text-secondary">
          <Loader2 className="h-4 w-4 animate-spin text-accent" />
          Checking what depends on {mod?.name}…
        </div>
      ) : (
        <>
          {breaking ? (
            <div className="rounded-md border border-red-500/40 bg-red-500/10 p-3">
              <div className="flex items-start gap-2.5">
                <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-red-400" />
                <div className="min-w-0">
                  <p className="text-sm font-medium text-text-primary">
                    This content is required by {required.length} other item
                    {required.length === 1 ? "" : "s"}
                  </p>
                  <p className="mt-0.5 text-xs text-text-secondary">
                    {mod?.name} is a dependency of the content listed below.{" "}
                    {verbing} it may stop them loading or break the next boot.
                  </p>
                </div>
              </div>
            </div>
          ) : (
            <div className="rounded-md border border-border bg-surface-2 p-3 text-sm text-text-secondary">
              {mode === "uninstall" ? (
                <>
                  <span className="text-text-primary">{mod?.name}</span> will be
                  deleted from{" "}
                  <span className="font-mono text-xs">{mod?.install_path}</span>{" "}
                  on the server. Nothing else installed requires it.
                </>
              ) : (
                <>
                  <span className="text-text-primary">{mod?.name}</span> will be
                  renamed to <span className="font-mono text-xs">.disabled</span>{" "}
                  so the server stops loading it. Nothing else installed requires
                  it.
                </>
              )}
            </div>
          )}

          {isError && (
            <p className="mt-3 text-xs text-warning">
              Couldn't check dependencies — the list below may be incomplete.
            </p>
          )}

          {/* What's going away */}
          {mod && (
            <>
              <p className="mt-4 mb-1.5 text-xs font-medium uppercase tracking-wide text-text-secondary">
                {verbing}
              </p>
              <ModCard
                serverId={serverId}
                name={mod.name}
                sourceId={mod.source_id ?? undefined}
                source={mod.source}
                version={mod.version}
              />
            </>
          )}

          {breaking && (
            <>
              <p className="mt-4 mb-1.5 text-xs font-medium uppercase tracking-wide text-text-secondary">
                Affected content
              </p>
              <div className="max-h-48 space-y-2 overflow-y-auto pr-1">
                {required.map((d) => (
                  <ModCard
                    key={d.mod_id}
                    serverId={serverId}
                    name={d.name}
                    sourceId={d.source_id}
                    source={d.source}
                    version={d.version}
                    note={d.enabled ? undefined : "already disabled"}
                  />
                ))}
              </div>
            </>
          )}

          {optional.length > 0 && (
            <p className="mt-3 text-xs text-text-secondary">
              <span className="text-text-primary">
                {optional.map((d) => d.name).join(", ")}
              </span>{" "}
              {optional.length === 1 ? "uses" : "use"} it optionally — they keep
              loading, but lose that integration.
            </p>
          )}

          {breaking && (
            <>
              <p className="mt-4 mb-1.5 text-xs font-medium uppercase tracking-wide text-text-secondary">
                What happens
              </p>
              <ul className="space-y-1 text-xs text-text-secondary">
                <li>• The affected content may fail to load or disable itself</li>
                <li>
                  • The server may crash, refuse to start, or behave unexpectedly
                </li>
                {mode === "uninstall" && (
                  <li>
                    • Re-installing {mod?.name} later restores the dependency
                  </li>
                )}
              </ul>

              <label className="mt-3 flex cursor-pointer items-start gap-3 rounded-md border border-border bg-surface-2 px-3 py-2.5">
                <input
                  type="checkbox"
                  className="mt-0.5"
                  checked={disableDependents}
                  onChange={(e) => setDisableDependents(e.target.checked)}
                />
                <div>
                  <p className="text-sm font-medium text-text-primary">
                    Disable the affected content too
                  </p>
                  <p className="text-xs text-text-secondary">
                    Recommended. Their jars stay on disk (renamed{" "}
                    <span className="font-mono">.disabled</span>) so the server
                    still boots, and you can switch them back on once the
                    dependency is back.
                  </p>
                </div>
              </label>
            </>
          )}

          {orphaning.length > 0 && (
            <p className="mt-3 flex items-start gap-2 text-xs text-text-secondary">
              <Unlink className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
              <span>
                Afterwards{" "}
                <span className="text-text-primary">{orphaning.join(", ")}</span>{" "}
                {orphaning.length === 1 ? "is" : "are"} no longer needed by
                anything — the Mods tab will flag{" "}
                {orphaning.length === 1 ? "it" : "them"} as removable.
              </span>
            </p>
          )}

          {impact && !impact.checked && (
            <p className="mt-3 text-xs text-text-secondary">
              This jar isn't linked to a project, so nothing can reference it in
              the dependency graph — check manually if other content needs it.
            </p>
          )}

          {impact && impact.unchecked > 0 && (
            <p className="mt-2 text-xs text-text-secondary">
              {impact.unchecked} other installed item
              {impact.unchecked === 1 ? "" : "s"} couldn't be checked (uploaded
              jars and CurseForge/SpigotMC content don't publish dependency data).
            </p>
          )}

          {breaking && (
            <div className="mt-4 flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-surface-2 px-3 py-2.5">
              <p className="min-w-0 flex-1 text-xs text-text-secondary">
                {backupStarted
                  ? "Backup started — you can restore the server if this goes wrong."
                  : "Back the server up first so you can restore it if this goes wrong."}
              </p>
              <Button
                size="sm"
                variant="outline"
                onClick={startBackup}
                loading={backupPending}
                disabled={backupStarted}
              >
                {!backupPending && <Archive className="h-3.5 w-3.5" />}
                {backupStarted ? "Backup queued" : "Create backup"}
              </Button>
            </div>
          )}
        </>
      )}

      <div className="mt-5 flex justify-end gap-3">
        <Button variant="outline" onClick={onClose} disabled={pending}>
          Cancel
        </Button>
        <Button
          variant="destructive"
          onClick={() => onConfirm({ disableDependents: breaking && disableDependents })}
          loading={pending}
          disabled={isLoading}
        >
          {!pending &&
            (mode === "uninstall" ? (
              <Trash2 className="h-4 w-4" />
            ) : (
              <Ban className="h-4 w-4" />
            ))}
          {breaking ? `${verb} anyway` : verb}
        </Button>
      </div>
    </Dialog>
  );
}

/** One mod as a card: icon, name, source badge, version. */
function ModCard({
  serverId,
  name,
  sourceId,
  source,
  version,
  note,
}: {
  serverId: string;
  name: string;
  sourceId?: string;
  source: string;
  version: string;
  note?: string;
}) {
  // Same query key the installed rows use, so the icon is already cached and
  // this costs nothing on a list the operator just looked at.
  const { data: project } = useQuery({
    queryKey: ["mod-project", source, sourceId],
    queryFn: () => api.mods.getProject(serverId, sourceId!, source),
    enabled: !!sourceId,
    staleTime: 10 * 60_000,
  });

  return (
    <div className="flex items-center gap-3 rounded-md border border-border bg-surface-2/50 px-3 py-2">
      {project?.icon_url ? (
        <img
          src={project.icon_url}
          alt=""
          className="h-9 w-9 flex-shrink-0 rounded-md object-cover"
        />
      ) : (
        <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-surface-2">
          <Package className="h-4 w-4 text-text-secondary" />
        </div>
      )}
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium text-text-primary">
          {project?.title ?? name}
        </p>
        <div className="flex items-center gap-2">
          <span
            className={`rounded border px-1.5 py-0.5 text-[10px] uppercase tracking-wide ${sourceBadgeClass(source)}`}
          >
            {source}
          </span>
          <span className="truncate text-xs text-text-secondary">{version}</span>
          {note && (
            <span className="flex-shrink-0 text-[10px] uppercase tracking-wide text-text-secondary">
              {note}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}
