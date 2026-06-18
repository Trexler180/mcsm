import { FolderTree, HardDrive, SlidersHorizontal, Terminal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { ResourceChart } from "@/components/charts/resource-chart";
import type { Server, Backup } from "@/lib/types";
import { Panel, StatTile, type ServerSection } from "./shared";

export function DashboardTab({
  server,
  backups,
  onSection,
}: {
  server: Server;
  backups: Backup[];
  onSection: (section: ServerSection) => void;
}) {
  const latestBackup = backups[0];
  const ram =
    server.ram_mb_max >= 1024 && server.ram_mb_max % 1024 === 0
      ? `${server.ram_mb_max / 1024} GB`
      : `${server.ram_mb_max} MB`;

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatTile
          label="Status"
          value={server.status}
          detail={
            server.status === "online" ? "Accepting commands" : "Not running"
          }
        />
        <StatTile
          label="Address"
          value={`:${server.port}`}
          detail="Server port"
        />
        <StatTile
          label="Memory"
          value={ram}
          detail={`Min ${server.ram_mb_min} MB`}
        />
        <StatTile
          label="Software"
          value={server.platform}
          detail={server.mc_version}
        />
      </div>

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-[1.3fr_1fr]">
        <Panel
          title="Resources"
          description="Live agent metrics for this server."
        >
          <ResourceChart
            serverId={server.id}
            ramMaxMb={server.ram_mb_max}
            status={server.status}
          />
        </Panel>
        <Panel title="Quick Actions">
          <div className="grid grid-cols-2 gap-2">
            <Button variant="outline" onClick={() => onSection("console")}>
              <Terminal className="h-4 w-4" /> Console
            </Button>
            <Button variant="outline" onClick={() => onSection("files")}>
              <FolderTree className="h-4 w-4" /> Files
            </Button>
            <Button variant="outline" onClick={() => onSection("options")}>
              <SlidersHorizontal className="h-4 w-4" /> Options
            </Button>
            <Button variant="outline" onClick={() => onSection("backups")}>
              <HardDrive className="h-4 w-4" /> Backups
            </Button>
          </div>
        </Panel>
      </div>

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        <Panel
          title="Server Details"
          description="Edit name, directory, and runtime in Options."
          onClick={() => onSection("options")}
        >
          <dl className="grid grid-cols-[120px_1fr] gap-y-2 text-sm">
            <dt className="text-text-secondary">Name</dt>
            <dd className="text-text-primary">{server.name}</dd>
            <dt className="text-text-secondary">Directory</dt>
            <dd className="truncate font-mono text-text-primary">
              {server.directory_path}
            </dd>
            <dt className="text-text-secondary">Java</dt>
            <dd className="font-mono text-text-primary">
              {server.java_binary}
            </dd>
            <dt className="text-text-secondary">Auto Start</dt>
            <dd className="text-text-primary">
              {server.auto_start ? "Enabled" : "Disabled"}
            </dd>
          </dl>
        </Panel>
        <Panel
          title="Latest Backup"
          description="View and manage all backups."
          onClick={() => onSection("backups")}
        >
          {latestBackup ? (
            <div className="space-y-2 text-sm">
              <div className="flex items-center justify-between">
                <span className="text-text-secondary">Status</span>
                <Badge
                  variant={
                    latestBackup.status === "success"
                      ? "success"
                      : latestBackup.status === "failed"
                        ? "error"
                        : "warning"
                  }
                >
                  {latestBackup.status}
                </Badge>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-text-secondary">Started</span>
                <span>
                  {new Date(latestBackup.started_at).toLocaleString()}
                </span>
              </div>
            </div>
          ) : (
            <p className="text-sm text-text-secondary">No backups yet.</p>
          )}
        </Panel>
      </div>
    </div>
  );
}
