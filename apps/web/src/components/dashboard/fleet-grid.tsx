import { ShieldAlert } from "lucide-react";
import { Card } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { Server } from "lucide-react";
import type { OverviewServer, ServerStatus } from "@/lib/types";

/**
 * Compact per-server tiles — status, platform/version, and an at-a-glance
 * conflict flag — replacing the old flat server list.
 */
export function FleetGrid({
  servers,
  onOpenServer,
}: {
  servers: OverviewServer[];
  onOpenServer: (id: string, tab?: string) => void;
}) {
  return (
    <Card>
      <div className="flex items-center gap-2 border-b border-border px-5 py-3">
        <h2 className="text-sm font-semibold text-text-primary">Fleet</h2>
        <span className="ml-auto text-xs text-text-secondary">
          {servers.length} server{servers.length === 1 ? "" : "s"}
        </span>
      </div>
      {servers.length === 0 ? (
        <EmptyState
          icon={Server}
          title="No servers yet"
          hint="Create a server to start managing it from here."
        />
      ) : (
        <div className="grid grid-cols-1 gap-px bg-border sm:grid-cols-2 lg:grid-cols-3">
          {servers.map((s) => (
            <button
              key={s.id}
              type="button"
              onClick={() => onOpenServer(s.id)}
              className="flex flex-col gap-2 bg-surface px-4 py-3 text-left transition-colors hover:bg-surface-2/60"
            >
              <div className="flex items-center justify-between gap-2">
                <span className="truncate text-sm font-medium text-text-primary">
                  {s.name}
                </span>
                <StatusBadge status={s.status as ServerStatus} />
              </div>
              <div className="flex items-center gap-2 text-xs text-text-secondary">
                <span className="min-w-0 truncate">
                  {s.platform} {s.mc_version}
                </span>
                {s.active_conflict && (
                  <span className="ml-auto inline-flex flex-shrink-0 items-center gap-1 text-red-400">
                    <ShieldAlert className="h-3 w-3 flex-shrink-0" /> conflict
                  </span>
                )}
              </div>
            </button>
          ))}
        </div>
      )}
    </Card>
  );
}
