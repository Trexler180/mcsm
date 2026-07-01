import {
  Loader2,
  X,
  ShieldCheck,
  CheckCircle2,
  XCircle,
  Undo2,
} from "lucide-react";
import type {
  ModUpdateRun,
  ModUpdateStep,
} from "@/lib/types";

export type BulkUpdateProgress = {
  total: number;
  completed: number;
  currentName: string;
};

// ── Safe auto-update banner ──────────────────────────────────────────
// Renders a running run's live phase + per-mod steps, or a finished run's
// outcome (with dismiss) until the user closes it.

export const RUN_PHASE_LABELS: Record<string, string> = {
  checking: "Checking for updates…",
  applying: "Applying updates…",
  verifying: "Restarting & verifying boot…",
  isolating: "Boot failed — isolating the broken update…",
  reverting: "Boot failed — reverting updates…",
  restoring: "Restoring server state…",
  done: "Finished",
};

export const RUN_STATUS_LABELS: Record<string, string> = {
  success: "Safe update finished",
  no_updates: "Everything is up to date",
  partial: "Safe update finished with issues",
  reverted: "Updates reverted — broken versions blocklisted",
  failed: "Safe update failed",
};

export function StepStatusIcon({ status }: { status: ModUpdateStep["status"] }) {
  switch (status) {
    case "updated":
      return <CheckCircle2 className="h-3.5 w-3.5 text-success shrink-0" />;
    case "reverted_skipped":
      return <Undo2 className="h-3.5 w-3.5 text-warning shrink-0" />;
    case "failed":
      return <XCircle className="h-3.5 w-3.5 text-danger shrink-0" />;
    default:
      return (
        <Loader2 className="h-3.5 w-3.5 animate-spin text-accent shrink-0" />
      );
  }
}

export function stepLabel(s: ModUpdateStep): string {
  switch (s.status) {
    case "updated":
      return `${s.from_version} → ${s.to_version}`;
    case "reverted_skipped":
      return `${s.to_version} broke the boot — reverted to ${s.from_version}, version blocklisted`;
    case "failed":
      return s.error || "update failed";
    default:
      return `${s.from_version} → ${s.to_version}…`;
  }
}

export function AutoUpdateBanner({
  run,
  onDismiss,
}: {
  run: ModUpdateRun;
  onDismiss?: () => void;
}) {
  const running = run.status === "running";
  const detail = run.detail;
  const headline = running
    ? RUN_PHASE_LABELS[detail?.phase ?? "checking"] || "Working…"
    : RUN_STATUS_LABELS[run.status] || "Safe update finished";
  const tone =
    run.status === "failed"
      ? "text-danger"
      : run.status === "reverted" || run.status === "partial"
        ? "text-warning"
        : "text-text-primary";
  const steps = detail?.mods ?? [];

  return (
    <div className="border-b border-border bg-surface px-4 py-3">
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2 text-xs">
          {running ? (
            <Loader2 className="h-4 w-4 animate-spin text-accent shrink-0" />
          ) : (
            <ShieldCheck className={`h-4 w-4 shrink-0 ${tone}`} />
          )}
          <span className={`font-medium ${tone}`}>{headline}</span>
          {detail?.message && (
            <span className="min-w-0 truncate text-text-secondary">
              {detail.message}
            </span>
          )}
        </div>
        {onDismiss && (
          <button
            onClick={onDismiss}
            className="text-text-secondary hover:text-text-primary shrink-0"
            title="Dismiss"
          >
            <X className="h-4 w-4" />
          </button>
        )}
      </div>
      {steps.length > 0 && (
        <ul className="mt-2 space-y-1">
          {steps.map((s) => (
            <li
              key={s.mod_id}
              className="flex items-center gap-2 text-xs text-text-secondary"
            >
              <StepStatusIcon status={s.status} />
              <span className="font-medium text-text-primary">{s.name}</span>
              <span className="min-w-0 truncate">{stepLabel(s)}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// sourceBadgeClass colors the installed-list source chip per origin: Modrinth
