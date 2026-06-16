import { useEffect, useState } from "react";
import {
  AlertTriangle,
  HardDrive,
  PlugZap,
  ServerCrash,
  ShieldAlert,
  ChevronRight,
  CheckCircle2,
  X,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Card } from "@/components/ui/card";
import { ageInDays } from "@/lib/time";
import type { Overview } from "@/lib/types";

const STALE_BACKUP_DAYS = 7;
const DISMISS_KEY = "cockpit:dismissed-attention";

type Severity = "high" | "warn";

interface AttentionItem {
  key: string;
  severity: Severity;
  icon: LucideIcon;
  label: string;
  detail: string;
  onClick: () => void;
}

const sevStyles: Record<Severity, { dot: string; icon: string }> = {
  high: { dot: "bg-red-500", icon: "text-red-400" },
  warn: { dot: "bg-amber-500", icon: "text-amber-400" },
};

function loadDismissed(): Set<string> {
  try {
    const raw = localStorage.getItem(DISMISS_KEY);
    return new Set(raw ? (JSON.parse(raw) as string[]) : []);
  } catch {
    return new Set();
  }
}

/**
 * Surfaces everything an operator should act on now — mod conflicts, crashes /
 * failed starts, stale backups, and offline nodes — each deep-linking into the
 * relevant place. Items can be dismissed (persisted), and dismissals self-clear
 * once the underlying signal goes away. Renders a healthy state when empty.
 */
export function AttentionCard({
  overview,
  onOpenServer,
  onOpenNodes,
}: {
  overview: Overview;
  onOpenServer: (id: string, tab?: string) => void;
  onOpenNodes: () => void;
}) {
  const [dismissed, setDismissed] = useState<Set<string>>(loadDismissed);

  const nameOf = (id: string) =>
    overview.servers.find((s) => s.id === id)?.name ?? "Unknown server";

  const items: AttentionItem[] = [];

  // Active mod conflicts.
  for (const c of overview.conflicts) {
    items.push({
      key: `conflict:${c.id}`,
      severity: "high",
      icon: ShieldAlert,
      label: `Mod conflict · ${nameOf(c.server_id)}`,
      detail: c.summary || "Conflicting mods detected",
      onClick: () => onOpenServer(c.server_id, "mods"),
    });
  }

  // Recent crashes / failed starts (error-level log events).
  const seenCrash = new Set<string>();
  for (const w of overview.warnings) {
    if (w.level !== "error" || seenCrash.has(w.server_id)) continue;
    seenCrash.add(w.server_id);
    items.push({
      key: `crash:${w.id}`,
      severity: "high",
      icon: ServerCrash,
      label: `Crash · ${nameOf(w.server_id)}`,
      detail: w.message,
      onClick: () => onOpenServer(w.server_id, "logs"),
    });
  }

  // Stale / missing backups. The key encodes the backup state so a dismissal
  // clears itself once a fresh backup is taken (and re-shows if it goes stale
  // again later).
  for (const s of overview.servers) {
    const age = ageInDays(s.last_backup_at);
    if (age === null || age > STALE_BACKUP_DAYS) {
      items.push({
        key: `backup:${s.id}:${s.last_backup_at ?? "none"}`,
        severity: "warn",
        icon: HardDrive,
        label: `No recent backup · ${s.name}`,
        detail:
          age === null
            ? "No successful backup yet"
            : `Last successful backup ${age}d ago`,
        onClick: () => onOpenServer(s.id, "backups"),
      });
    }
  }

  // Offline nodes.
  for (const n of overview.nodes) {
    if (!n.online) {
      items.push({
        key: `node:${n.id}:${n.last_seen ?? "never"}`,
        severity: "high",
        icon: PlugZap,
        label: `Node offline · ${n.name}`,
        detail: "No heartbeat from this node",
        onClick: onOpenNodes,
      });
    }
  }

  // Drop dismissed keys that no longer correspond to a live signal, so the
  // stored set stays bounded and self-heals.
  useEffect(() => {
    const live = new Set(items.map((i) => i.key));
    const pruned = [...dismissed].filter((k) => live.has(k));
    if (pruned.length !== dismissed.size) {
      const next = new Set(pruned);
      setDismissed(next);
      try {
        localStorage.setItem(DISMISS_KEY, JSON.stringify(pruned));
      } catch {
        // ignore storage failures
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [overview]);

  const dismiss = (key: string) => {
    setDismissed((prev) => {
      const next = new Set(prev);
      next.add(key);
      try {
        localStorage.setItem(DISMISS_KEY, JSON.stringify([...next]));
      } catch {
        // ignore storage failures
      }
      return next;
    });
  };

  const visible = items.filter((i) => !dismissed.has(i.key));

  if (visible.length === 0) {
    return (
      <Card className="flex items-center gap-3 px-5 py-4">
        <CheckCircle2 className="h-5 w-5 text-green-400" />
        <div>
          <p className="text-sm font-medium text-text-primary">
            Everything looks healthy
          </p>
          <p className="text-xs text-text-secondary">
            No conflicts, crashes, stale backups, or offline nodes.
          </p>
        </div>
      </Card>
    );
  }

  // High-severity items first.
  visible.sort((a, b) =>
    a.severity === b.severity ? 0 : a.severity === "high" ? -1 : 1,
  );

  return (
    <Card>
      <div className="flex items-center gap-2 border-b border-border px-5 py-3">
        <AlertTriangle className="h-4 w-4 text-amber-400" />
        <h2 className="text-sm font-semibold text-text-primary">
          Needs attention
        </h2>
        <span className="ml-auto text-xs text-text-secondary">
          {visible.length}
        </span>
      </div>
      <div className="max-h-80 divide-y divide-border overflow-y-auto">
        {visible.map((item) => {
          const s = sevStyles[item.severity];
          return (
            <div
              key={item.key}
              className="group flex items-center transition-colors hover:bg-surface-2/60"
            >
              <button
                type="button"
                onClick={item.onClick}
                className="flex min-w-0 flex-1 items-center gap-3 px-5 py-3 text-left"
              >
                <span
                  className={`h-2 w-2 flex-shrink-0 rounded-full ${s.dot}`}
                />
                <item.icon className={`h-4 w-4 flex-shrink-0 ${s.icon}`} />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium text-text-primary">
                    {item.label}
                  </p>
                  <p className="truncate text-xs text-text-secondary">
                    {item.detail}
                  </p>
                </div>
                <ChevronRight className="h-4 w-4 flex-shrink-0 text-text-secondary" />
              </button>
              <button
                type="button"
                onClick={() => dismiss(item.key)}
                aria-label="Dismiss"
                title="Dismiss"
                className="flex-shrink-0 px-3 py-3 text-text-secondary/50 hover:text-text-primary"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
          );
        })}
      </div>
    </Card>
  );
}
