import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { FileArchive, FileText, FileWarning, RefreshCw, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";

type LogFileItem = {
  path: string;
  name: string;
  kind: "log" | "crash";
  size: number;
  modified: string;
};

function formatLogFileSize(bytes: number) {
  if (bytes <= 0) return "-";
  if (bytes >= 1_048_576) return `${(bytes / 1_048_576).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${bytes} B`;
}

function logFileName(path: string) {
  return path.split("/").filter(Boolean).pop() ?? path;
}

async function readGzipLog(serverId: string, path: string) {
  if (!("DecompressionStream" in window)) {
    throw new Error("Gzip extraction is not supported in this browser.");
  }

  const bytes = await api.files.readBytes(serverId, path);
  const stream = new Blob([bytes as BlobPart])
    .stream()
    .pipeThrough(new DecompressionStream("gzip"));
  return new Response(stream).text();
}

export function LogsTab({ serverId }: { serverId: string }) {
  const [path, setPath] = useState("/logs/latest.log");
  const [search, setSearch] = useState("");
  const [extractedLog, setExtractedLog] = useState<{
    path: string;
    content: string;
  } | null>(null);
  const [extractingLog, setExtractingLog] = useState(false);
  const [extractError, setExtractError] = useState<string | null>(null);
  const {
    data: logFiles = [],
    isLoading: isLoadingFiles,
    refetch: refetchFiles,
  } = useQuery({
    queryKey: ["log-files", serverId],
    queryFn: async () => {
      const roots: Array<{ path: string; kind: LogFileItem["kind"] }> = [
        { path: "/logs", kind: "log" },
        { path: "/crash-reports", kind: "crash" },
      ];

      const groups = await Promise.all(
        roots.map(async (root) => {
          try {
            const tree = await api.files.tree(serverId, root.path);
            return tree.entries
              .filter((entry) => entry.type === "file")
              .map((entry) => {
                const fullPath = `${root.path}/${entry.path}`.replace(
                  /\/+/g,
                  "/",
                );
                return {
                  path: fullPath,
                  name: logFileName(fullPath),
                  kind: root.kind,
                  size: entry.size,
                  modified: entry.modified,
                };
              });
          } catch {
            return [];
          }
        }),
      );

      return groups
        .flat()
        .sort(
          (a, b) =>
            new Date(b.modified).getTime() - new Date(a.modified).getTime(),
      );
    },
  });

  useEffect(() => {
    setExtractedLog(null);
    setExtractError(null);
  }, [path]);

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ["file-content", serverId, path],
    queryFn: () => api.files.readContent(serverId, path),
    enabled: path.trim().length > 0 && !path.toLowerCase().endsWith(".gz"),
    retry: false,
  });

  const isCompressedLog = path.trim().toLowerCase().endsWith(".gz");
  const visibleLog =
    isCompressedLog && extractedLog?.path === path ? extractedLog.content : data;

  const filteredLogFiles = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) return logFiles;
    return logFiles.filter(
      (file) =>
        file.path.toLowerCase().includes(query) ||
        file.name.toLowerCase().includes(query) ||
        file.kind.includes(query),
    );
  }, [logFiles, search]);

  const selectedLogFile = logFiles.find((file) => file.path === path);

  const refreshAll = () => {
    refetchFiles();
    if (isCompressedLog) {
      setExtractedLog(null);
      setExtractError(null);
    } else {
      refetch();
    }
  };

  const extractCompressedLog = async () => {
    const currentPath = path.trim();
    if (!currentPath) return;

    setExtractingLog(true);
    setExtractError(null);
    try {
      const content = await readGzipLog(serverId, currentPath);
      setExtractedLog({ path: currentPath, content });
    } catch (err) {
      setExtractedLog(null);
      setExtractError(err instanceof Error ? err.message : "Failed to extract log.");
    } finally {
      setExtractingLog(false);
    }
  };

  return (
    <div className="grid h-full min-h-0 grid-rows-[14rem_minmax(0,1fr)] grid-cols-[minmax(0,1fr)] gap-4 p-4 sm:p-5 xl:grid-rows-1 xl:grid-cols-[20rem_minmax(0,1fr)]">
      {/* The file list and viewer stack (capped list height so the viewer keeps
          the bulk of the screen). They only sit side by side from xl, since the
          server view's two sidebars keep this pane narrow below that.
          grid-cols-[minmax(0,1fr)] pins the single stacked column to the pane
          width — without it the implicit auto column grows to the widest log
          line and scrolls the whole tab sideways. min-w-0 on the children below
          is the matching guard so their content scrolls internally instead. */}
      <div className="flex min-w-0 min-h-0 flex-col rounded-md border border-border bg-surface">
        <div className="border-b border-border p-3">
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-secondary" />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search logs and crashes"
              className="pl-9"
            />
          </div>
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          {isLoadingFiles ? (
            <div className="flex justify-center py-8">
              <div className="h-5 w-5 animate-spin rounded-full border-2 border-accent border-t-transparent" />
            </div>
          ) : filteredLogFiles.length === 0 ? (
            <div className="px-4 py-8 text-center text-sm text-text-secondary">
              {logFiles.length === 0
                ? "No logs or crash reports found."
                : "No matching logs or crash reports."}
            </div>
          ) : (
            <div className="divide-y divide-border">
              {filteredLogFiles.map((file) => {
                const active = file.path === path;
                return (
                  <button
                    key={file.path}
                    type="button"
                    onClick={() => setPath(file.path)}
                    className={[
                      "flex w-full items-start gap-3 px-3 py-3 text-left transition-colors",
                      active ? "bg-accent/10" : "hover:bg-surface-2",
                    ].join(" ")}
                  >
                    {file.kind === "crash" ? (
                      <FileWarning className="mt-0.5 h-4 w-4 shrink-0 text-red-400" />
                    ) : file.path.toLowerCase().endsWith(".gz") ? (
                      <FileArchive className="mt-0.5 h-4 w-4 shrink-0 text-text-secondary" />
                    ) : (
                      <FileText className="mt-0.5 h-4 w-4 shrink-0 text-text-secondary" />
                    )}
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium text-text-primary">
                        {file.name}
                      </span>
                      <span className="mt-1 block truncate text-xs text-text-secondary">
                        {file.path}
                      </span>
                      <span className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-text-secondary">
                        <Badge
                          variant={file.kind === "crash" ? "error" : "muted"}
                        >
                          {file.kind === "crash" ? "crash" : "log"}
                        </Badge>
                        <span>{formatLogFileSize(file.size)}</span>
                        <span>{new Date(file.modified).toLocaleString()}</span>
                      </span>
                    </span>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      </div>

      <div className="flex min-w-0 min-h-0 flex-col">
        <div className="mb-3 flex flex-col gap-2 sm:flex-row sm:items-center">
          <Input
            value={path}
            onChange={(e) => setPath(e.target.value)}
            className="min-w-0 flex-1 font-mono"
          />
          <Button
            variant="outline"
            onClick={refreshAll}
            className="shrink-0"
          >
            <RefreshCw className="h-4 w-4" />
            Refresh
          </Button>
          {isCompressedLog && (
            <Button
              onClick={extractCompressedLog}
              disabled={extractingLog || !path.trim()}
              className="shrink-0"
            >
              <FileArchive className="h-4 w-4" />
              {extractingLog ? "Extracting..." : "Extract view"}
            </Button>
          )}
        </div>
        {selectedLogFile && (
          <div className="mb-3 flex flex-wrap items-center gap-2 text-xs text-text-secondary">
            <Badge variant={selectedLogFile.kind === "crash" ? "error" : "muted"}>
              {selectedLogFile.kind === "crash" ? "crash report" : "log file"}
            </Badge>
            <span>{formatLogFileSize(selectedLogFile.size)}</span>
            <span>
              Modified {new Date(selectedLogFile.modified).toLocaleString()}
            </span>
          </div>
        )}
        <div className="min-h-0 flex-1 overflow-auto rounded-md border border-border bg-[#0f0f0f] p-4 font-mono text-xs leading-5 text-text-primary">
          {isCompressedLog && extractError ? (
            <div className="text-red-400">{extractError}</div>
          ) : isCompressedLog && extractedLog?.path !== path ? (
            <div className="text-text-secondary">
              This log is compressed. Use Extract view to temporarily decompress
              it here.
            </div>
          ) : isLoading ? (
            <div className="text-text-secondary">Loading log...</div>
          ) : isError ? (
            <div className="text-text-secondary">
              No log file found at {path}.
            </div>
          ) : (
            <pre className="whitespace-pre-wrap break-words">{visibleLog}</pre>
          )}
        </div>
      </div>
    </div>
  );
}

