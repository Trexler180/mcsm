import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowRight,
  ArrowDownToLine,
  ArrowUpToLine,
  CheckCircle2,
  Loader2,
  Minus,
  RotateCcw,
  ShieldQuestion,
  TriangleAlert,
  XCircle,
} from "lucide-react";
import { Dialog } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type {
  MigrationModStep,
  ModCompat,
  ModCompatStatus,
  VersionMigration,
} from "@/lib/types";

/**
 * Change the server's Minecraft version (upgrade or downgrade). Previews how the
 * installed content handles the target — the ratio of mods that have / don't
 * have a compatible build — then applies the change: back up, bump the version,
 * move compatible mods, disable the rest, and restore automatically if the boot
 * is unhealthy. CurseForge and custom jars can't be checked and are left for
 * manual review.
 */

const STATUS_META: Record<
  ModCompatStatus,
  { label: string; chip: string; bar: string; dot: string }
> = {
  compatible: {
    label: "Will update",
    chip: "text-green-400 border-green-400/40 bg-green-400/10",
    bar: "bg-green-500",
    dot: "bg-green-500",
  },
  supported: {
    label: "Already compatible",
    chip: "text-sky-400 border-sky-400/40 bg-sky-400/10",
    bar: "bg-sky-500",
    dot: "bg-sky-500",
  },
  incompatible: {
    label: "Will be disabled",
    chip: "text-red-400 border-red-400/40 bg-red-400/10",
    bar: "bg-red-500",
    dot: "bg-red-500",
  },
  unmanaged: {
    label: "Manual review",
    chip: "text-zinc-400 border-zinc-400/40 bg-zinc-400/10",
    bar: "bg-zinc-500",
    dot: "bg-zinc-500",
  },
  unknown: {
    label: "Check failed",
    chip: "text-amber-400 border-amber-400/40 bg-amber-400/10",
    bar: "bg-amber-500",
    dot: "bg-amber-500",
  },
};

// Display order: best news first, then the things needing attention.
const ORDER: ModCompatStatus[] = [
  "compatible",
  "supported",
  "incompatible",
  "unknown",
  "unmanaged",
];

const PHASE_LABELS: Record<string, string> = {
  checking: "Checking compatibility…",
  backup: "Creating a restore point…",
  applying: "Applying the version change…",
  verifying: "Restarting and watching the boot…",
  restoring: "Boot failed — restoring the backup…",
  done: "Done",
};

const RUN_STATUS: Record<
  VersionMigration["status"],
  { label: string; tone: string }
> = {
  running: { label: "Migration in progress", tone: "text-text-secondary" },
  success: { label: "Migration complete", tone: "text-green-400" },
  partial: { label: "Migrated with some failures", tone: "text-amber-400" },
  reverted: { label: "Reverted — server unchanged", tone: "text-amber-400" },
  failed: { label: "Migration failed", tone: "text-red-400" },
};

function StatusIcon({ status }: { status: ModCompatStatus }) {
  switch (status) {
    case "compatible":
      return <ArrowRight className="h-3.5 w-3.5 text-green-400" />;
    case "supported":
      return <CheckCircle2 className="h-3.5 w-3.5 text-sky-400" />;
    case "incompatible":
      return <XCircle className="h-3.5 w-3.5 text-red-400" />;
    case "unknown":
      return <TriangleAlert className="h-3.5 w-3.5 text-amber-400" />;
    default:
      return <ShieldQuestion className="h-3.5 w-3.5 text-zinc-400" />;
  }
}

function StepIcon({ status }: { status: MigrationModStep["status"] }) {
  switch (status) {
    case "done":
      return <CheckCircle2 className="h-3.5 w-3.5 text-green-400" />;
    case "failed":
      return <XCircle className="h-3.5 w-3.5 text-red-400" />;
    case "planned":
      return <Loader2 className="h-3.5 w-3.5 animate-spin text-text-secondary" />;
    default:
      return <Minus className="h-3.5 w-3.5 text-text-secondary opacity-50" />;
  }
}

function compareMc(a: string, b: string): number {
  const pa = a.split(/[.\-+ ]/).map((n) => parseInt(n, 10));
  const pb = b.split(/[.\-+ ]/).map((n) => parseInt(n, 10));
  for (let i = 0; i < Math.max(pa.length, pb.length); i += 1) {
    const x = pa[i] || 0;
    const y = pb[i] || 0;
    if (Number.isNaN(x) || Number.isNaN(y)) return 0;
    if (x !== y) return x - y;
  }
  return 0;
}

export function VersionCheckDialog({
  open,
  onClose,
  serverId,
  platform,
  currentMcVersion,
}: {
  open: boolean;
  onClose: () => void;
  serverId: string;
  platform: string;
  currentMcVersion: string;
}) {
  const qc = useQueryClient();
  const { error } = useNotifications();
  const [target, setTarget] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [runId, setRunId] = useState<string | null>(null);
  const settledRef = useRef(false);

  const reset = () => {
    setTarget("");
    setConfirming(false);
    setRunId(null);
    settledRef.current = false;
  };

  const { data: gameVersions = [] } = useQuery({
    queryKey: ["mc-versions", platform],
    queryFn: () => api.minecraft.versions(platform, true),
    enabled: open,
  });

  const {
    data: result,
    isFetching,
    isError,
    error: checkError,
  } = useQuery({
    queryKey: ["version-check", serverId, target],
    queryFn: () => api.mods.versionCheck(serverId, target),
    enabled: open && target !== "" && runId === null,
  });

  // Poll the migration run while one is active.
  const { data: run } = useQuery({
    queryKey: ["migration", serverId, runId],
    queryFn: () => api.servers.migration(serverId, runId as string),
    enabled: open && runId !== null,
    refetchInterval: (q) =>
      q.state.data && q.state.data.status === "running" ? 1500 : false,
  });

  // When the run reaches a terminal state, refresh the data it touched once.
  useEffect(() => {
    if (!run || run.status === "running" || settledRef.current) return;
    settledRef.current = true;
    qc.invalidateQueries({ queryKey: ["mods", serverId] });
    qc.invalidateQueries({ queryKey: ["mod-updates", serverId] });
    qc.invalidateQueries({ queryKey: ["server", serverId] });
    qc.invalidateQueries({ queryKey: ["backups", serverId] });
  }, [run, qc, serverId]);

  const migrate = useMutation({
    mutationFn: () => api.servers.migrate(serverId, target),
    onSuccess: (m) => {
      setConfirming(false);
      setRunId(m.id);
    },
    onError: (e: Error) => error("Could not start migration", e.message),
  });

  const direction = useMemo(() => {
    if (!target || target === currentMcVersion) return "same";
    return compareMc(target, currentMcVersion) > 0 ? "up" : "down";
  }, [target, currentMcVersion]);

  const grouped = useMemo(() => {
    const g = new Map<ModCompatStatus, ModCompat[]>();
    for (const m of result?.mods ?? []) {
      const arr = g.get(m.status) ?? [];
      arr.push(m);
      g.set(m.status, arr);
    }
    return g;
  }, [result]);

  const total = result?.total ?? 0;
  const ready =
    (result?.counts.compatible ?? 0) + (result?.counts.supported ?? 0);
  const toUpdate = result?.counts.compatible ?? 0;
  const toDisable = result?.counts.incompatible ?? 0;
  const needsReview =
    (result?.counts.unmanaged ?? 0) + (result?.counts.unknown ?? 0);

  const closeAll = () => {
    reset();
    onClose();
  };

  const handleClose = () => {
    // Don't lose track of an in-flight migration: allow closing, it keeps
    // running server-side and can be reopened from history later.
    closeAll();
  };

  return (
    <Dialog
      open={open}
      onClose={handleClose}
      title="Change Minecraft version"
      description="Preview how your installed content handles a version change, then apply it safely."
      titleIcon={<ArrowDownToLine className="h-5 w-5 text-accent" />}
      className="max-w-2xl"
    >
      {runId ? (
        /* ── Live migration progress ─────────────────────────────── */
        <MigrationProgress run={run} target={target} onClose={closeAll} />
      ) : (
        <>
          {/* Target picker */}
          <div className="flex flex-wrap items-end gap-3">
            <div className="min-w-[14rem] flex-1">
              <label className="mb-1 block text-xs font-medium text-text-secondary">
                Target version
              </label>
              <select
                value={target}
                onChange={(e) => {
                  setTarget(e.target.value);
                  setConfirming(false);
                }}
                className="w-full rounded-md border border-border bg-surface-2 px-3 py-2 text-sm text-text-primary"
              >
                <option value="">Select a version…</option>
                {gameVersions.map((v) => (
                  <option key={v.version} value={v.version}>
                    {v.version}
                    {v.version === currentMcVersion ? " (current)" : ""}
                    {v.stable ? "" : " — snapshot"}
                  </option>
                ))}
              </select>
            </div>
            <div className="pb-2 text-sm text-text-secondary">
              <span className="font-mono">{currentMcVersion}</span>
              {direction !== "same" && target && (
                <>
                  {direction === "up" ? (
                    <ArrowUpToLine className="mx-1.5 inline h-3.5 w-3.5 text-green-400" />
                  ) : (
                    <ArrowDownToLine className="mx-1.5 inline h-3.5 w-3.5 text-amber-400" />
                  )}
                  <span className="font-mono text-text-primary">{target}</span>
                  <span className="ml-1.5 text-xs">
                    ({direction === "up" ? "upgrade" : "downgrade"})
                  </span>
                </>
              )}
            </div>
          </div>

          {/* Body */}
          <div className="mt-4">
            {!target ? (
              <p className="py-10 text-center text-sm text-text-secondary">
                Pick a target version to see the compatibility breakdown.
              </p>
            ) : isFetching ? (
              <p className="flex items-center justify-center gap-2 py-10 text-sm text-text-secondary">
                <Loader2 className="h-4 w-4 animate-spin" /> Checking{" "}
                {total ? `${total} ` : ""}items against {target}…
              </p>
            ) : isError ? (
              <p className="py-10 text-center text-sm text-red-400">
                {(checkError as Error)?.message || "Compatibility check failed"}
              </p>
            ) : result && total === 0 ? (
              <p className="py-10 text-center text-sm text-text-secondary">
                No managed content installed — changing the version only swaps the
                server jar.
              </p>
            ) : result ? (
              <>
                {/* Ratio summary */}
                <div className="mb-1 flex items-baseline justify-between">
                  <span className="text-sm font-medium text-text-primary">
                    {ready} of {total} ready for {target}
                  </span>
                  <span className="text-xs text-text-secondary">
                    {total > 0 ? Math.round((ready / total) * 100) : 0}%
                  </span>
                </div>
                <div className="flex h-3 overflow-hidden rounded-full border border-border">
                  {ORDER.map((s) => {
                    const n = result.counts[s] ?? 0;
                    if (!n) return null;
                    return (
                      <div
                        key={s}
                        className={STATUS_META[s].bar}
                        style={{ width: `${(n / total) * 100}%` }}
                        title={`${n} ${STATUS_META[s].label}`}
                      />
                    );
                  })}
                </div>

                {/* Legend / counts */}
                <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1.5">
                  {ORDER.map((s) => {
                    const n = result.counts[s] ?? 0;
                    if (!n) return null;
                    return (
                      <span
                        key={s}
                        className="flex items-center gap-1.5 text-xs text-text-secondary"
                      >
                        <span
                          className={`h-2 w-2 rounded-full ${STATUS_META[s].dot}`}
                        />
                        {n} {STATUS_META[s].label}
                      </span>
                    );
                  })}
                </div>

                {/* Per-mod list, grouped by status */}
                <div className="mt-4 max-h-60 space-y-4 overflow-y-auto pr-1">
                  {ORDER.filter((s) => grouped.has(s)).map((s) => (
                    <div key={s}>
                      <div className="mb-1.5 flex items-center gap-2">
                        <span
                          className={`rounded-full border px-2 py-0.5 text-[11px] font-medium ${STATUS_META[s].chip}`}
                        >
                          {STATUS_META[s].label}
                        </span>
                        <span className="text-xs text-text-secondary">
                          {grouped.get(s)!.length}
                        </span>
                      </div>
                      <div className="divide-y divide-border rounded-md border border-border">
                        {grouped.get(s)!.map((m) => (
                          <div
                            key={m.mod_id}
                            className="flex items-center gap-2 px-3 py-2 text-sm"
                          >
                            <StatusIcon status={m.status} />
                            <span className="min-w-0 flex-1 truncate text-text-primary">
                              {m.name}
                              {!m.enabled && (
                                <span className="ml-2 text-xs text-text-secondary">
                                  (disabled)
                                </span>
                              )}
                              {m.pinned && (
                                <span className="ml-2 text-xs text-text-secondary">
                                  (pinned)
                                </span>
                              )}
                            </span>
                            <span className="flex items-center gap-1.5 font-mono text-xs text-text-secondary">
                              <span className="truncate">
                                {m.current_version}
                              </span>
                              {m.target_version &&
                              m.target_version !== m.current_version ? (
                                <>
                                  <ArrowRight className="h-3 w-3 flex-shrink-0" />
                                  <span className="truncate text-green-400">
                                    {m.target_version}
                                  </span>
                                </>
                              ) : (
                                <Minus className="h-3 w-3 flex-shrink-0 opacity-40" />
                              )}
                            </span>
                          </div>
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              </>
            ) : null}
          </div>

          {/* Footer: confirm + apply */}
          {target && !isFetching && !isError && result && (
            <div className="mt-5 border-t border-border pt-4">
              {confirming ? (
                <div className="space-y-3">
                  <div className="rounded-md border border-amber-400/40 bg-amber-400/10 p-3 text-sm text-text-secondary">
                    <p className="font-medium text-text-primary">
                      Change this server to {target}?
                    </p>
                    <p className="mt-1">
                      A backup is taken first. {toUpdate} mod
                      {toUpdate === 1 ? "" : "s"} will be updated and {toDisable}{" "}
                      disabled. If the server doesn't boot cleanly, the backup is
                      restored automatically.
                      {needsReview > 0 &&
                        ` ${needsReview} item${needsReview === 1 ? "" : "s"} can't be checked and won't be touched — review ${needsReview === 1 ? "it" : "them"} manually.`}
                    </p>
                  </div>
                  <div className="flex justify-end gap-3">
                    <Button
                      variant="outline"
                      onClick={() => setConfirming(false)}
                      disabled={migrate.isPending}
                    >
                      Cancel
                    </Button>
                    <Button onClick={() => migrate.mutate()} loading={migrate.isPending}>
                      <ArrowRightIcon direction={direction} /> Change to {target}
                    </Button>
                  </div>
                </div>
              ) : (
                <div className="flex items-center justify-between gap-3">
                  <p className="text-xs text-text-secondary">
                    Applying backs up the server first and reverts automatically
                    on a failed boot.
                  </p>
                  <div className="flex gap-3">
                    <Button variant="outline" onClick={handleClose}>
                      Close
                    </Button>
                    <Button
                      onClick={() => setConfirming(true)}
                      disabled={direction === "same"}
                      title={
                        direction === "same"
                          ? "Pick a different version"
                          : undefined
                      }
                    >
                      Change version
                    </Button>
                  </div>
                </div>
              )}
            </div>
          )}
        </>
      )}
    </Dialog>
  );
}

function ArrowRightIcon({ direction }: { direction: string }) {
  if (direction === "down")
    return <ArrowDownToLine className="h-4 w-4" />;
  return <ArrowUpToLine className="h-4 w-4" />;
}

function MigrationProgress({
  run,
  target,
  onClose,
}: {
  run: VersionMigration | undefined;
  target: string;
  onClose: () => void;
}) {
  if (!run) {
    return (
      <p className="flex items-center justify-center gap-2 py-12 text-sm text-text-secondary">
        <Loader2 className="h-4 w-4 animate-spin" /> Starting migration…
      </p>
    );
  }

  const detail = run.detail;
  const running = run.status === "running";
  const meta = RUN_STATUS[run.status];
  const steps = detail?.mods?.filter((m) => m.action !== "unchanged") ?? [];

  return (
    <div>
      <div className="flex items-center gap-3 rounded-md border border-border bg-surface-2 p-3">
        {running ? (
          <Loader2 className="h-5 w-5 animate-spin text-accent" />
        ) : run.status === "success" ? (
          <CheckCircle2 className="h-5 w-5 text-green-400" />
        ) : run.status === "reverted" ? (
          <RotateCcw className="h-5 w-5 text-amber-400" />
        ) : run.status === "partial" ? (
          <TriangleAlert className="h-5 w-5 text-amber-400" />
        ) : (
          <XCircle className="h-5 w-5 text-red-400" />
        )}
        <div className="min-w-0">
          <p className={`text-sm font-medium ${meta.tone}`}>{meta.label}</p>
          <p className="truncate text-xs text-text-secondary">
            {detail?.message ||
              PHASE_LABELS[detail?.phase ?? "checking"] ||
              `Migrating to ${target}`}
          </p>
        </div>
      </div>

      {steps.length > 0 && (
        <div className="mt-4 max-h-72 divide-y divide-border overflow-y-auto rounded-md border border-border">
          {steps.map((m) => (
            <div
              key={m.mod_id}
              className="flex items-center gap-2 px-3 py-2 text-sm"
            >
              <StepIcon status={m.status} />
              <span className="min-w-0 flex-1 truncate text-text-primary">
                {m.name}
                <span className="ml-2 text-xs text-text-secondary">
                  {m.action === "disable" ? "disable" : "update"}
                </span>
                {m.error && (
                  <span className="ml-2 text-xs text-red-400">{m.error}</span>
                )}
              </span>
              {m.action === "update" && m.to_version && (
                <span className="flex items-center gap-1.5 font-mono text-xs text-text-secondary">
                  <span className="truncate">{m.from_version}</span>
                  <ArrowRight className="h-3 w-3 flex-shrink-0" />
                  <span className="truncate text-green-400">{m.to_version}</span>
                </span>
              )}
            </div>
          ))}
        </div>
      )}

      <div className="mt-5 flex justify-end">
        <Button variant={running ? "outline" : "default"} onClick={onClose}>
          {running ? "Run in background" : "Done"}
        </Button>
      </div>
    </div>
  );
}
