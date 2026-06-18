import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowDownToLine,
  ArrowRight,
  ArrowUpToLine,
  CheckCircle2,
  Clock,
  Loader2,
  Minus,
  RotateCcw,
  ShieldQuestion,
  TriangleAlert,
  XCircle,
} from "lucide-react";
import { Panel } from "@/components/servers/shared";
import { SoftwareOptionsPanel } from "@/components/servers/options-properties";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import { relativeTime } from "@/lib/time";
import type {
  MigrationModStep,
  ModCompat,
  ModCompatStatus,
  Server,
  VersionMigration,
} from "@/lib/types";

const STATUS_META: Record<
  ModCompatStatus,
  { label: string; chip: string; bar: string; dot: string; tip: string }
> = {
  compatible: {
    label: "Will update",
    chip: "text-green-400 border-green-400/40 bg-green-400/10",
    bar: "bg-green-500",
    dot: "bg-green-500",
    tip: "A build for the target exists and will be installed.",
  },
  supported: {
    label: "Already compatible",
    chip: "text-sky-400 border-sky-400/40 bg-sky-400/10",
    bar: "bg-sky-500",
    dot: "bg-sky-500",
    tip: "The installed build already supports the target.",
  },
  incompatible: {
    label: "Will be disabled",
    chip: "text-red-400 border-red-400/40 bg-red-400/10",
    bar: "bg-red-500",
    dot: "bg-red-500",
    tip: "No build for the target yet — disabled, not removed.",
  },
  unknown: {
    label: "Check failed",
    chip: "text-amber-400 border-amber-400/40 bg-amber-400/10",
    bar: "bg-amber-500",
    dot: "bg-amber-500",
    tip: "The lookup failed this time — left untouched, review manually.",
  },
  unmanaged: {
    label: "Manual review",
    chip: "text-zinc-400 border-zinc-400/40 bg-zinc-400/10",
    bar: "bg-zinc-500",
    dot: "bg-zinc-500",
    tip: "CurseForge / custom jars can't be checked — left untouched.",
  },
};

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

/**
 * The Version tab: the smart, mod-aware version migration up top, with the raw
 * runtime controls (platform switch, loader, Java binary, JVM args) below for
 * manual changes and platform swaps the migration flow doesn't cover.
 */
export function VersionTab({ server }: { server: Server }) {
  return (
    <div className="max-w-3xl space-y-5">
      <VersionMigrationPanel server={server} />
      <SoftwareOptionsPanel server={server} />
    </div>
  );
}

/**
 * First-class interface for changing a server's Minecraft version (upgrade or
 * downgrade). Previews how every installed mod handles the target, then applies
 * the change atomically: back up, bump the version, move compatible mods, disable
 * incompatible ones, restart and watch the boot, restoring the backup if it comes
 * up unhealthy. Resumes a migration already in flight and shows recent history.
 */
export function VersionMigrationPanel({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { error } = useNotifications();
  const [target, setTarget] = useState("");
  const [snapshots, setSnapshots] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [runId, setRunId] = useState<string | null>(null);
  const settledRef = useRef(false);

  const { data: gameVersions = [] } = useQuery({
    queryKey: ["mc-versions", server.platform, snapshots],
    queryFn: () => api.minecraft.versions(server.platform, snapshots),
    staleTime: 30 * 60_000,
  });

  // History + auto-resume: if a migration is already running, attach to it.
  const { data: history = [] } = useQuery({
    queryKey: ["migrations", server.id],
    queryFn: () => api.servers.migrations(server.id, 10),
    refetchInterval: runId ? false : 5000,
  });
  useEffect(() => {
    if (runId) return;
    const latest = history[0];
    if (latest && latest.status === "running") setRunId(latest.id);
  }, [history, runId]);

  const {
    data: result,
    isFetching,
    isError,
    error: checkError,
  } = useQuery({
    queryKey: ["version-check", server.id, target],
    queryFn: () => api.mods.versionCheck(server.id, target),
    enabled: target !== "" && runId === null,
  });

  const { data: run } = useQuery({
    queryKey: ["migration", server.id, runId],
    queryFn: () => api.servers.migration(server.id, runId as string),
    enabled: runId !== null,
    refetchInterval: (q) =>
      q.state.data && q.state.data.status === "running" ? 1500 : false,
  });

  useEffect(() => {
    if (!run || run.status === "running" || settledRef.current) return;
    settledRef.current = true;
    qc.invalidateQueries({ queryKey: ["mods", server.id] });
    qc.invalidateQueries({ queryKey: ["mod-updates", server.id] });
    qc.invalidateQueries({ queryKey: ["server", server.id] });
    qc.invalidateQueries({ queryKey: ["backups", server.id] });
    qc.invalidateQueries({ queryKey: ["migrations", server.id] });
  }, [run, qc, server.id]);

  const migrate = useMutation({
    mutationFn: () => api.servers.migrate(server.id, target),
    onSuccess: (m) => {
      setConfirming(false);
      setRunId(m.id);
    },
    onError: (e: Error) => error("Could not start migration", e.message),
  });

  const direction = useMemo(() => {
    if (!target || target === server.mc_version) return "same";
    return compareMc(target, server.mc_version) > 0 ? "up" : "down";
  }, [target, server.mc_version]);

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

  const dismissRun = () => {
    setRunId(null);
    setConfirming(false);
    setTarget("");
    settledRef.current = false;
  };

  // ── Active migration view ──────────────────────────────────────────
  if (runId) {
    return (
      <Panel
        title="Change Minecraft version"
        description={`Migrating from ${server.mc_version}${run ? ` to ${run.to_mc_version}` : ""}.`}
      >
        <MigrationProgress run={run} onDone={dismissRun} />
      </Panel>
    );
  }

  // ── Picker + preview view ──────────────────────────────────────────
  return (
    <div className="space-y-5">
      <Panel
        title="Change Minecraft version"
        description="Move this server to another version (upgrade or downgrade). Compatible mods are updated, incompatible ones are disabled, and a backup is taken first so a bad boot rolls back automatically."
      >
        {/* Current → target */}
        <div className="flex flex-wrap items-end gap-4">
          <div>
            <p className="text-xs font-medium text-text-secondary">Current</p>
            <p className="mt-1 font-mono text-lg text-text-primary">
              {server.mc_version}
            </p>
            <p className="text-xs capitalize text-text-secondary">
              {server.platform}
              {server.loader_version ? ` · ${server.loader_version}` : ""}
            </p>
          </div>

          {direction === "up" ? (
            <ArrowUpToLine className="mb-2 h-5 w-5 text-green-400" />
          ) : direction === "down" ? (
            <ArrowDownToLine className="mb-2 h-5 w-5 text-amber-400" />
          ) : (
            <ArrowRight className="mb-2 h-5 w-5 text-text-secondary opacity-40" />
          )}

          <div className="min-w-[14rem] flex-1">
            <div className="mb-1 flex items-center justify-between">
              <label className="text-xs font-medium text-text-secondary">
                Target version
              </label>
              <label className="flex cursor-pointer items-center gap-1.5 text-xs text-text-secondary">
                <input
                  type="checkbox"
                  checked={snapshots}
                  onChange={(e) => setSnapshots(e.target.checked)}
                />
                Snapshots
              </label>
            </div>
            <select
              value={target}
              onChange={(e) => {
                setTarget(e.target.value);
                setConfirming(false);
              }}
              className="w-full rounded-md border border-border bg-surface-2 px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
            >
              <option value="">Select a version…</option>
              {gameVersions.map((v) => (
                <option key={v.version} value={v.version}>
                  {v.version}
                  {v.version === server.mc_version ? " (current)" : ""}
                  {v.stable ? "" : " — snapshot"}
                </option>
              ))}
            </select>
          </div>
        </div>

        {/* Compatibility */}
        {target && direction !== "same" && (
          <div className="mt-5">
            {isFetching ? (
              <p className="flex items-center justify-center gap-2 py-8 text-sm text-text-secondary">
                <Loader2 className="h-4 w-4 animate-spin" /> Checking content
                against {target}…
              </p>
            ) : isError ? (
              <p className="py-8 text-center text-sm text-red-400">
                {(checkError as Error)?.message || "Compatibility check failed"}
              </p>
            ) : result ? (
              <>
                {total === 0 ? (
                  <p className="rounded-md border border-border bg-surface-2 p-3 text-sm text-text-secondary">
                    No managed content installed — changing the version only swaps
                    the server runtime.
                  </p>
                ) : (
                  <>
                    {/* Summary count cards */}
                    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                      {ORDER.map((s) => {
                        const n = result.counts[s] ?? 0;
                        if (!n) return null;
                        return (
                          <div
                            key={s}
                            className="rounded-md border border-border bg-surface-2 p-2.5"
                            title={STATUS_META[s].tip}
                          >
                            <div className="flex items-center gap-1.5">
                              <span
                                className={`h-2 w-2 rounded-full ${STATUS_META[s].dot}`}
                              />
                              <span className="text-lg font-semibold text-text-primary">
                                {n}
                              </span>
                            </div>
                            <p className="mt-0.5 text-xs text-text-secondary">
                              {STATUS_META[s].label}
                            </p>
                          </div>
                        );
                      })}
                    </div>

                    {/* Ratio bar */}
                    <div className="mt-3 mb-1 flex items-baseline justify-between">
                      <span className="text-sm font-medium text-text-primary">
                        {ready} of {total} ready for {target}
                      </span>
                      <span className="text-xs text-text-secondary">
                        {total > 0 ? Math.round((ready / total) * 100) : 0}%
                      </span>
                    </div>
                    <div className="flex h-2.5 overflow-hidden rounded-full border border-border">
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

                    {/* Grouped lists */}
                    <div className="mt-4 space-y-4">
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
                )}

                {/* Apply / confirm */}
                <div className="mt-5 border-t border-border pt-4">
                  {confirming ? (
                    <div className="space-y-3">
                      <div className="rounded-md border border-amber-400/40 bg-amber-400/10 p-3 text-sm text-text-secondary">
                        <p className="font-medium text-text-primary">
                          Change this server to {target}?
                        </p>
                        <p className="mt-1">
                          A backup is taken first. {toUpdate} mod
                          {toUpdate === 1 ? "" : "s"} will be updated and{" "}
                          {toDisable} disabled. If the server doesn't boot
                          cleanly, the backup is restored automatically.
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
                        <Button
                          onClick={() => migrate.mutate()}
                          loading={migrate.isPending}
                        >
                          {direction === "down" ? (
                            <ArrowDownToLine className="h-4 w-4" />
                          ) : (
                            <ArrowUpToLine className="h-4 w-4" />
                          )}
                          Change to {target}
                        </Button>
                      </div>
                    </div>
                  ) : (
                    <div className="flex items-center justify-between gap-3">
                      <p className="text-xs text-text-secondary">
                        Applying backs up the server first and reverts
                        automatically on a failed boot.
                      </p>
                      <Button onClick={() => setConfirming(true)}>
                        Change version
                      </Button>
                    </div>
                  )}
                </div>
              </>
            ) : null}
          </div>
        )}
      </Panel>

      {history.length > 0 && (
        <Panel title="Recent migrations">
          <div className="divide-y divide-border">
            {history.map((h) => (
              <button
                key={h.id}
                onClick={() => setRunId(h.id)}
                className="flex w-full items-center gap-3 px-1 py-2 text-left text-sm hover:bg-surface-2"
              >
                <span className="font-mono text-xs text-text-secondary">
                  {h.from_mc_version}
                </span>
                <ArrowRight className="h-3 w-3 text-text-secondary" />
                <span className="font-mono text-xs text-text-primary">
                  {h.to_mc_version}
                </span>
                <span className={`ml-2 text-xs ${RUN_STATUS[h.status].tone}`}>
                  {RUN_STATUS[h.status].label}
                </span>
                <span className="ml-auto flex items-center gap-1 text-xs text-text-secondary">
                  <Clock className="h-3 w-3" />
                  {relativeTime(h.started_at)}
                </span>
              </button>
            ))}
          </div>
        </Panel>
      )}
    </div>
  );
}

function MigrationProgress({
  run,
  onDone,
}: {
  run: VersionMigration | undefined;
  onDone: () => void;
}) {
  if (!run) {
    return (
      <p className="flex items-center justify-center gap-2 py-12 text-sm text-text-secondary">
        <Loader2 className="h-4 w-4 animate-spin" /> Loading migration…
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
              "Working…"}
          </p>
        </div>
      </div>

      {steps.length > 0 && (
        <div className="mt-4 divide-y divide-border rounded-md border border-border">
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
        <Button variant={running ? "outline" : "default"} onClick={onDone}>
          {running ? "Run in background" : "Done"}
        </Button>
      </div>
    </div>
  );
}
