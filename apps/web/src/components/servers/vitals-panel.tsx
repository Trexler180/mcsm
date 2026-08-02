import { useQuery } from "@tanstack/react-query";
import { Activity } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { api } from "@/lib/api";
import type { ServerVitals } from "@/lib/types";
import { Panel, StatTile } from "./shared";

// A snapshot arrives every 15s; polling the API at the same cadence costs the
// Minecraft server nothing (the agent answers from memory). Data older than
// four heartbeats is presented as stale rather than silently shown as current.
const POLL_MS = 15_000;
const STALE_MS = 60_000;

// Tick health, judged the way server admins do: a full 20 TPS with tick times
// inside the 50ms budget is healthy; TPS holding but the budget nearly spent
// means lag is imminent; TPS visibly down means players feel it already.
function health(v: ServerVitals): {
  label: string;
  variant: "success" | "warning" | "error";
} {
  const tps = v.tps?.m1 ?? 0;
  const mspt = v.mspt?.avg ?? 0;
  if (tps >= 19.5 && mspt <= 45) return { label: "Healthy", variant: "success" };
  if (tps >= 15) return { label: "Lagging", variant: "warning" };
  return { label: "Overloaded", variant: "error" };
}

// Minecraft caps at 20 TPS but rolling averages can float a hair above; showing
// "20.1" would just make the number look broken.
function fmtTps(n: number): string {
  return Math.min(n, 20).toFixed(1);
}

function fmtMb(mb: number): string {
  return mb >= 1024 ? `${(mb / 1024).toFixed(1)} GB` : `${mb} MB`;
}

function fmtCount(n: number): string {
  return n.toLocaleString();
}

// Live TPS / tick-time / heap / world tiles, fed by the helper mod.
//
// Renders nothing at all for servers that have never reported — a vanilla or
// Paper server's dashboard looks exactly as it did before this panel existed.
export function VitalsPanel({
  serverId,
  online,
}: {
  serverId: string;
  online: boolean;
}) {
  const { data } = useQuery({
    queryKey: ["vitals", serverId],
    queryFn: () => api.servers.vitals(serverId),
    enabled: online,
    refetchInterval: POLL_MS,
  });

  // No snapshot has ever arrived (no mod, not linked yet, or server offline):
  // stay invisible rather than showing a panel of dashes.
  if (!online || !data?.tps || !data?.mspt) return null;

  const stale = (data.snapshot_age_ms ?? 0) > STALE_MS;
  const status = health(data);

  return (
    <Panel
      title="Live Vitals"
      description={
        data.mod_version
          ? `Reported by the helper mod v${data.mod_version} every 15s.`
          : "Reported by the helper mod every 15s."
      }
      actions={
        stale || !data.linked ? (
          <Badge variant="warning" className="flex-shrink-0">
            <Activity className="mr-1 h-3 w-3" />
            {stale ? "Stale data" : "Reconnecting"}
          </Badge>
        ) : (
          <Badge variant={status.variant} className="flex-shrink-0">
            <Activity className="mr-1 h-3 w-3" />
            {status.label}
          </Badge>
        )
      }
    >
      <div
        className={`grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5${
          stale ? " opacity-60" : ""
        }`}
      >
        <StatTile
          label="TPS"
          value={fmtTps(data.tps.m1)}
          detail={`5m ${fmtTps(data.tps.m5)} · 15m ${fmtTps(data.tps.m15)}`}
        />
        <StatTile
          label="Tick time"
          value={`${data.mspt.avg.toFixed(1)} ms`}
          detail={`p95 ${data.mspt.p95.toFixed(1)} · p99 ${data.mspt.p99.toFixed(1)} ms`}
        />
        {data.heap && (
          <StatTile
            label="Heap"
            value={fmtMb(data.heap.used_mb)}
            detail={
              data.heap.max_mb > 0 ? `of ${fmtMb(data.heap.max_mb)}` : "in use"
            }
          />
        )}
        <StatTile
          label="Chunks"
          value={fmtCount(data.chunks ?? 0)}
          detail="loaded"
        />
        <StatTile
          label="Entities"
          value={fmtCount(data.entities ?? 0)}
          detail="total"
        />
      </div>
    </Panel>
  );
}
