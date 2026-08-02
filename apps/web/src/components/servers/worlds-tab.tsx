import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Globe2, Upload } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { SkeletonList } from "@/components/ui/skeleton";
import { api } from "@/lib/api";
import type { ServerStatus } from "@/lib/types";
import { parseProperties } from "./options/properties-schema";
import { Panel } from "./shared";
import { WorldInfoDialog } from "./world-info-dialog";
import { WorldUploadDialog } from "./world-upload-dialog";

// A world is a top-level folder holding a level.dat, which is what Minecraft
// itself looks for. Matching on the folder name instead would miss every
// uploaded or downloaded map that isn't called "world".
const WORLD_MARKER = "/level.dat";

// Statuses in which the server has its world files open. Anything else and the
// directory is ours to rewrite.
const RUNNING: ServerStatus[] = ["starting", "online", "stopping"];

export function WorldsTab({
  serverId,
  status,
}: {
  serverId: string;
  status: ServerStatus;
}) {
  const [selected, setSelected] = useState<string | null>(null);
  const [uploading, setUploading] = useState(false);

  const { data: root, isLoading } = useQuery({
    queryKey: ["files", serverId, "/"],
    queryFn: () => api.files.list(serverId, "/"),
  });

  // Depth 2 lists the root's files plus one level down, which is exactly deep
  // enough to spot each candidate folder's level.dat without walking region
  // directories. One request covers every folder in the root.
  const { data: tree, isError: treeFailed } = useQuery({
    queryKey: ["file-tree", serverId, "/", 2],
    queryFn: () => api.files.tree(serverId, "/", { depth: 2, max: 20000 }),
  });

  const { data: properties } = useQuery({
    queryKey: ["file-content", serverId, "/server.properties"],
    queryFn: () => api.files.readContent(serverId, "/server.properties"),
    retry: false,
  });
  const activeWorld =
    parseProperties(properties ?? "")["level-name"]?.trim() || "world";

  const dirs = (root?.entries ?? []).filter((entry) => entry.type === "dir");
  const withLevelDat = new Set(
    (tree?.entries ?? [])
      .filter((entry) => entry.path.endsWith(WORLD_MARKER))
      .map((entry) => entry.path.slice(0, -WORLD_MARKER.length))
      .filter((name) => !name.includes("/")),
  );

  // If the tree walk failed we can't prove which folders hold a level.dat, so
  // fall back to the old name heuristic rather than showing nothing.
  const worlds = treeFailed
    ? dirs.filter((entry) => /world|nether|end/i.test(entry.name))
    : dirs.filter((entry) => withLevelDat.has(entry.name));

  return (
    <div className="p-4 sm:p-6">
      <Panel
        title="Worlds"
        description="World folders in the server root, detected by their level.dat."
        actions={
          <Button
            size="sm"
            variant="outline"
            onClick={() => setUploading(true)}
            className="flex-shrink-0"
          >
            <Upload className="h-3.5 w-3.5" />
            Upload world
          </Button>
        }
      >
        {isLoading ? (
          <SkeletonList rows={3} />
        ) : worlds.length === 0 ? (
          <EmptyState
            icon={Globe2}
            title="No worlds yet"
            hint="Worlds appear here once the server has generated one, or you can upload a zipped world folder."
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
                    <p className="flex min-w-0 items-center gap-2 text-sm font-medium text-text-primary">
                      <span className="truncate">{world.name}</span>
                      {world.name === activeWorld && (
                        <Badge variant="success">Active</Badge>
                      )}
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
      {uploading && (
        <WorldUploadDialog
          serverId={serverId}
          existingWorlds={worlds.map((d) => d.name)}
          existingFolders={dirs.map((d) => d.name)}
          activeWorld={activeWorld}
          serverRunning={RUNNING.includes(status)}
          onClose={() => setUploading(false)}
        />
      )}
    </div>
  );
}
