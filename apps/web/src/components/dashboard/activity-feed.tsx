import { Activity, AlertTriangle } from "lucide-react";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { actionLabel, actionSeverity } from "@/lib/audit";
import { relativeTime } from "@/lib/time";
import type { AuditEntry, LogEvent } from "@/lib/types";

/**
 * Recent activity for the cockpit: the latest audit events, with the newest
 * error-level log warnings called out at the top.
 */
export function ActivityFeed({
  activity,
  warnings,
  nameOf,
}: {
  activity: AuditEntry[];
  warnings: LogEvent[];
  nameOf: (serverId: string | null) => string;
}) {
  const topWarnings = warnings.filter((w) => w.level === "error").slice(0, 3);

  return (
    <Card>
      <div className="border-b border-border px-5 py-3">
        <h2 className="text-sm font-semibold text-text-primary">
          Recent activity
        </h2>
      </div>

      {topWarnings.length > 0 && (
        <div className="divide-y divide-border border-b border-border bg-red-950/10">
          {topWarnings.map((w) => (
            <div key={`w-${w.id}`} className="flex items-start gap-3 px-5 py-2.5">
              <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-red-400" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm text-text-primary">{w.message}</p>
                <p className="text-xs text-text-secondary">
                  {nameOf(w.server_id)} · {relativeTime(w.created_at)}
                </p>
              </div>
            </div>
          ))}
        </div>
      )}

      {activity.length === 0 ? (
        <EmptyState
          icon={Activity}
          title="No activity yet"
          hint="Actions across your servers will show up here."
        />
      ) : (
        <div className="divide-y divide-border">
          {activity.map((e) => {
            const high = actionSeverity(e.action) === "high";
            return (
              <div key={e.id} className="flex items-center gap-3 px-5 py-2.5">
                <span
                  className={`h-1.5 w-1.5 flex-shrink-0 rounded-full ${
                    high ? "bg-red-500" : "bg-text-secondary/40"
                  }`}
                />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm text-text-primary">
                    {actionLabel(e.action)}
                    {e.server_id ? (
                      <span className="text-text-secondary">
                        {" "}
                        · {nameOf(e.server_id)}
                      </span>
                    ) : null}
                  </p>
                </div>
                <span className="flex-shrink-0 text-xs text-text-secondary">
                  {relativeTime(e.created_at)}
                </span>
              </div>
            );
          })}
        </div>
      )}
    </Card>
  );
}
