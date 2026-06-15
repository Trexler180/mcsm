import { useQuery } from "@tanstack/react-query";
import { Globe2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { api } from "@/lib/api";
import { Panel } from "./shared";

export function WorldsTab({ serverId }: { serverId: string }) {
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
          <div className="py-10 text-center text-sm text-text-secondary">
            No world folders detected yet.
          </div>
        ) : (
          <div className="divide-y divide-border rounded-md border border-border">
            {worlds.map((world) => (
              <div
                key={world.name}
                className="flex items-center justify-between px-4 py-3"
              >
                <div className="flex items-center gap-3">
                  <Globe2 className="h-4 w-4 text-text-secondary" />
                  <div>
                    <p className="text-sm font-medium text-text-primary">
                      {world.name}
                    </p>
                    <p className="text-xs text-text-secondary">
                      Modified {new Date(world.modified).toLocaleString()}
                    </p>
                  </div>
                </div>
                <Badge variant="muted">folder</Badge>
              </div>
            ))}
          </div>
        )}
      </Panel>
    </div>
  );
}
