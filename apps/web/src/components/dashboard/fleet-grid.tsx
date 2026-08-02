import { ShieldAlert, Server } from "lucide-react";
import { clsx } from "clsx";
import { Card } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { groupServersByFolder, folderAccent } from "@/lib/folders";
import type { OverviewServer, ServerFolder, ServerStatus } from "@/lib/types";

/**
 * Compact per-server tiles — status, platform/version, and an at-a-glance
 * conflict flag — replacing the old flat server list. Once folders exist the
 * tiles are split under folder headers so the fleet reads the same way here as
 * it does on the servers page.
 */
export function FleetGrid({
  servers,
  folders = [],
  onOpenServer,
}: {
  servers: OverviewServer[];
  folders?: ServerFolder[];
  onOpenServer: (id: string, tab?: string) => void;
}) {
  // Only fold the grid into sections once there is something to fold into;
  // otherwise a single "Ungrouped" header is pure noise.
  const grouped = folders.length > 0;
  const groups = grouped ? groupServersByFolder(servers, folders) : [];

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
      ) : grouped ? (
        groups
          // An empty folder earns a header on the servers page (you file things
          // into it there); on the dashboard it is just a gap.
          .filter((g) => g.servers.length > 0)
          .map(({ folder, servers: inFolder }) => (
            <div key={folder?.id ?? "__ungrouped"}>
              <div className="flex items-center gap-2 border-b border-border bg-surface-2/30 px-5 py-1.5">
                <span
                  className={clsx(
                    "h-2 w-2 flex-shrink-0 rounded-full",
                    folderAccent(folder?.color).dot,
                  )}
                />
                <span className="truncate text-xs font-medium text-text-secondary">
                  {folder?.name ?? "Ungrouped"}
                </span>
                <span className="ml-auto flex-shrink-0 text-xs tabular-nums text-text-secondary">
                  {inFolder.length}
                </span>
              </div>
              <ServerTiles servers={inFolder} onOpenServer={onOpenServer} />
            </div>
          ))
      ) : (
        <ServerTiles servers={servers} onOpenServer={onOpenServer} />
      )}
    </Card>
  );
}

function ServerTiles({
  servers,
  onOpenServer,
}: {
  servers: OverviewServer[];
  onOpenServer: (id: string, tab?: string) => void;
}) {
  return (
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
  );
}
