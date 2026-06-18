import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowRight,
  ArrowDownToLine,
  ArrowUpToLine,
  CheckCircle2,
  Loader2,
  Minus,
  ShieldQuestion,
  XCircle,
} from "lucide-react";
import { Dialog } from "@/components/ui/dialog";
import { api } from "@/lib/api";
import type { ModCompat, ModCompatStatus } from "@/lib/types";

/**
 * Read-only preview of how the installed mods would fare if the server moved to
 * a different Minecraft version (upgrade or downgrade). Shows the ratio of mods
 * that have / don't have a compatible build for the chosen target, and what the
 * eventual migration would do to each one. Applying the change lands in a later
 * phase; this dialog only inspects.
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

function StatusIcon({ status }: { status: ModCompatStatus }) {
  switch (status) {
    case "compatible":
      return <ArrowRight className="h-3.5 w-3.5 text-green-400" />;
    case "supported":
      return <CheckCircle2 className="h-3.5 w-3.5 text-sky-400" />;
    case "incompatible":
      return <XCircle className="h-3.5 w-3.5 text-red-400" />;
    case "unknown":
      return <Loader2 className="h-3.5 w-3.5 text-amber-400" />;
    default:
      return <ShieldQuestion className="h-3.5 w-3.5 text-zinc-400" />;
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
  const [target, setTarget] = useState("");

  const { data: gameVersions = [] } = useQuery({
    queryKey: ["mc-versions", platform],
    queryFn: () => api.minecraft.versions(platform, true),
    enabled: open,
  });

  const {
    data: result,
    isFetching,
    isError,
    error,
  } = useQuery({
    queryKey: ["version-check", serverId, target],
    queryFn: () => api.mods.versionCheck(serverId, target),
    enabled: open && target !== "",
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

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Change Minecraft version"
      description="Preview how your installed content handles a version change before committing."
      titleIcon={<ArrowDownToLine className="h-5 w-5 text-accent" />}
      className="max-w-2xl"
    >
      {/* Target picker */}
      <div className="flex flex-wrap items-end gap-3">
        <div className="flex-1 min-w-[14rem]">
          <label className="mb-1 block text-xs font-medium text-text-secondary">
            Target version
          </label>
          <select
            value={target}
            onChange={(e) => setTarget(e.target.value)}
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
            {(error as Error)?.message || "Compatibility check failed"}
          </p>
        ) : result && total === 0 ? (
          <p className="py-10 text-center text-sm text-text-secondary">
            No managed content installed — nothing to check.
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
            <div className="mt-4 max-h-72 space-y-4 overflow-y-auto pr-1">
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
                          <span className="truncate">{m.current_version}</span>
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

            <p className="mt-4 rounded-md border border-border bg-surface-2 p-3 text-xs text-text-secondary">
              This is a preview only. Applying a version change — bumping the
              server, moving compatible content, and disabling the rest behind a
              backup — is coming in a follow-up. CurseForge and custom jars can't
              be checked automatically and are listed for manual review.
            </p>
          </>
        ) : null}
      </div>
    </Dialog>
  );
}
