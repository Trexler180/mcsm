import { Server as ServerIcon } from "lucide-react";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { relativeTime } from "@/lib/time";
import type { OverviewNode } from "@/lib/types";

function formatMemory(mb: number | null): string {
  if (!mb || mb <= 0) return "—";
  return mb >= 1024 ? `${(mb / 1024).toFixed(0)} GB` : `${mb} MB`;
}

export function NodeHealthCard({ nodes }: { nodes: OverviewNode[] }) {
  return (
    <Card>
      <div className="border-b border-border px-5 py-3">
        <h2 className="text-sm font-semibold text-text-primary">Node health</h2>
      </div>
      {nodes.length === 0 ? (
        <EmptyState
          icon={ServerIcon}
          title="No nodes"
          hint="Register a node to run servers on it."
        />
      ) : (
        <div className="max-h-80 divide-y divide-border overflow-y-auto">
          {nodes.map((n) => (
            <div
              key={n.id}
              className="flex items-center gap-3 px-5 py-3"
            >
              <span
                className={`h-2 w-2 flex-shrink-0 rounded-full ${
                  n.online ? "bg-green-400" : "bg-red-500"
                }`}
              />
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium text-text-primary">
                  {n.name}
                </p>
                <p className="text-xs text-text-secondary">
                  {n.online ? "Online" : "Offline"} · seen{" "}
                  {relativeTime(n.last_seen)}
                </p>
              </div>
              <span className="flex-shrink-0 font-mono text-xs text-text-secondary">
                {formatMemory(n.memory_mb)}
              </span>
            </div>
          ))}
        </div>
      )}
    </Card>
  );
}
