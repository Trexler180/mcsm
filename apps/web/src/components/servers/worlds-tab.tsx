import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Globe2 } from "lucide-react";
import { EmptyState } from "@/components/ui/empty-state";
import { api } from "@/lib/api";
import { Panel } from "./shared";
import { WorldInfoDialog } from "./world-info-dialog";

export function WorldsTab({ serverId }: { serverId: string }) {
  const [selected, setSelected] = useState<string | null>(null);
  const { data: root, isLoading } = useQuery({
    queryKey: ["files", serverId, "/"],
    queryFn: () => api.files.list(serverId, "/"),
  });

  const worlds =
    root?.entries.filter(
      (entry) =>
        entry.type === "dir" &&
        (entry.name === "world" ||
          entry.name.toLowerCase().includes("world") ||
          entry.name.toLowerCase().includes("nether") ||
          entry.name.toLowerCase().includes("end")),
    ) ?? [];

  return (
    <div className="p-4 sm:p-6">
      <Panel
        title="Worlds"
        description="Detected world folders in the server root."
      >
        {isLoading ? (
          <div className="flex justify-center py-8">
            <div className="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin" />
          </div>
        ) : worlds.length === 0 ? (
          <EmptyState
            icon={Globe2}
            title="No world folders detected"
            hint="Worlds appear here once the server has run and generated a world directory."
          />
        ) : (
          <div className="divide-y divide-border rounded-md border border-border">
            {worlds.map((world) => (
              <button
                key={world.name}
                type="button"
                onClick={() => setSelected(world.name)}
                className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-surface-2/40"
              >
                <div className="flex min-w-0 items-center gap-3">
                  <Globe2 className="h-4 w-4 flex-shrink-0 text-text-secondary" />
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium text-text-primary">
                      {world.name}
                    </p>
                    <p className="truncate text-xs text-text-secondary">
                      Modified {new Date(world.modified).toLocaleString()}
                    </p>
                  </div>
                </div>
                <ChevronRight className="h-4 w-4 flex-shrink-0 text-text-secondary" />
              </button>
            ))}
          </div>
        )}
      </Panel>
      {selected && (
        <WorldInfoDialog
          serverId={serverId}
          worldName={selected}
          onClose={() => setSelected(null)}
        />
      )}
    </div>
  );
}
