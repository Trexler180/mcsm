import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Folder,
  File,
  ChevronRight,
  Upload,
  Download,
  Trash2,
  FolderPlus,
  RefreshCw,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { FileEntry } from "@/lib/types";

interface FileBrowserProps {
  serverId: string;
  onFileSelect: (path: string) => void;
}

function formatSize(bytes: number): string {
  if (bytes === 0) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let i = 0;
  let v = bytes;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

export function FileBrowser({ serverId, onFileSelect }: FileBrowserProps) {
  const [currentPath, setCurrentPath] = useState("/");
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const qc = useQueryClient();
  const { success, error } = useNotifications();

  const {
    data: listing,
    isLoading,
    refetch,
  } = useQuery({
    queryKey: ["files", serverId, currentPath],
    queryFn: () => api.files.list(serverId, currentPath),
  });

  const deleteMutation = useMutation({
    mutationFn: (path: string) => api.files.delete(serverId, path),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["files", serverId, currentPath] });
      success("Deleted");
      setDeleteTarget(null);
    },
    onError: (e: Error) => error("Delete failed", e.message),
  });

  const mkdirMutation = useMutation({
    mutationFn: () => {
      const name = prompt("Folder name:");
      if (!name) return Promise.reject(new Error("cancelled"));
      return api.files.mkdir(
        serverId,
        currentPath === "/" ? "/" + name : currentPath + "/" + name,
      );
    },
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["files", serverId, currentPath] }),
    onError: (e: Error) => {
      if (e.message !== "cancelled") error("Create folder failed", e.message);
    },
  });

  const navigate = (entry: FileEntry) => {
    if (entry.type === "dir") {
      const next =
        currentPath === "/" ? "/" + entry.name : currentPath + "/" + entry.name;
      setCurrentPath(next);
    } else {
      const filePath =
        currentPath === "/" ? "/" + entry.name : currentPath + "/" + entry.name;
      onFileSelect(filePath);
    }
  };

  const pathParts = currentPath.split("/").filter(Boolean);

  const handleUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files;
    if (!files) return;
    Array.from(files).forEach((f) =>
      api.files
        .upload(serverId, currentPath, f)
        .then(() => {
          success(`Uploaded ${f.name}`);
          qc.invalidateQueries({ queryKey: ["files", serverId, currentPath] });
        })
        .catch((err: Error) => error("Upload failed", err.message)),
    );
    e.target.value = "";
  };

  return (
    <div className="flex flex-col h-full min-w-0">
      {/* Toolbar */}
      <div className="flex items-center justify-between gap-2 px-4 py-2 border-b border-border bg-surface">
        {/* Breadcrumb */}
        <nav className="flex min-w-0 items-center gap-1 text-sm overflow-x-auto">
          <button
            onClick={() => setCurrentPath("/")}
            className="text-text-secondary hover:text-text-primary transition-colors"
          >
            /
          </button>
          {pathParts.map((part, i) => {
            const to = "/" + pathParts.slice(0, i + 1).join("/");
            return (
              <span key={to} className="flex items-center gap-1">
                <ChevronRight className="h-3.5 w-3.5 text-text-secondary" />
                <button
                  onClick={() => setCurrentPath(to)}
                  className="text-text-secondary hover:text-text-primary transition-colors"
                >
                  {part}
                </button>
              </span>
            );
          })}
        </nav>

        <div className="flex items-center gap-1.5 flex-shrink-0">
          <Button
            size="sm"
            variant="ghost"
            onClick={() => refetch()}
            title="Refresh"
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => mkdirMutation.mutate()}
            title="New folder"
          >
            <FolderPlus className="h-3.5 w-3.5" />
          </Button>
          <label
            title="Upload files"
            className="cursor-pointer inline-flex items-center justify-center h-7 px-3 text-xs rounded text-text-secondary hover:text-text-primary hover:bg-surface-2 transition-colors"
          >
            <Upload className="h-3.5 w-3.5" />
            <input
              type="file"
              multiple
              className="hidden"
              onChange={handleUpload}
            />
          </label>
        </div>
      </div>

      {/* File list */}
      <div className="flex-1 min-w-0 overflow-auto">
        {isLoading ? (
          <div className="flex justify-center py-8">
            <div className="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin" />
          </div>
        ) : (
          <table className="w-full table-fixed text-sm">
            <thead>
              <tr className="border-b border-border">
                <th className="text-left px-4 py-2 text-xs font-medium text-text-secondary">
                  Name
                </th>
                <th className="text-right px-3 py-2 text-xs font-medium text-text-secondary w-20">
                  Size
                </th>
                <th className="text-right px-4 py-2 text-xs font-medium text-text-secondary w-20">
                  Actions
                </th>
              </tr>
            </thead>
            <tbody>
              {currentPath !== "/" && (
                <tr
                  className="border-b border-border/50 hover:bg-surface-2/50 cursor-pointer"
                  onClick={() => {
                    const parts = currentPath.split("/").filter(Boolean);
                    parts.pop();
                    setCurrentPath(
                      parts.length === 0 ? "/" : "/" + parts.join("/"),
                    );
                  }}
                >
                  <td className="px-4 py-2.5 text-text-secondary">
                    <span className="flex min-w-0 items-center gap-2">
                      <Folder className="h-4 w-4 text-yellow-500 flex-shrink-0" />
                      <span className="truncate">..</span>
                    </span>
                  </td>
                  <td />
                  <td />
                </tr>
              )}
              {(listing?.entries ?? []).map((entry) => (
                <tr
                  key={entry.name}
                  className="border-b border-border/50 hover:bg-surface-2/50 cursor-pointer"
                  onClick={() => navigate(entry)}
                >
                  <td className="px-4 py-2.5 min-w-0">
                    <span className="flex min-w-0 items-center gap-2">
                      {entry.type === "dir" ? (
                        <Folder className="h-4 w-4 text-yellow-500 flex-shrink-0" />
                      ) : (
                        <File className="h-4 w-4 text-text-secondary flex-shrink-0" />
                      )}
                      <span className="text-text-primary truncate">
                        {entry.name}
                      </span>
                    </span>
                  </td>
                  <td className="px-3 py-2.5 text-right text-text-secondary">
                    {entry.type === "dir" ? "—" : formatSize(entry.size)}
                  </td>
                  <td
                    className="px-4 py-2.5 text-right"
                    onClick={(e) => e.stopPropagation()}
                  >
                    <div className="flex items-center justify-end gap-1">
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          void api.files.download(
                            serverId,
                            currentPath === "/"
                              ? "/" + entry.name
                              : currentPath + "/" + entry.name,
                          );
                        }}
                        title={`Download ${entry.name}`}
                        aria-label={`Download ${entry.name}`}
                        className="p-1 hover:text-text-primary text-text-secondary rounded"
                      >
                        <Download className="h-3.5 w-3.5" />
                      </button>
                      <button
                        onClick={() =>
                          setDeleteTarget(
                            currentPath === "/"
                              ? "/" + entry.name
                              : currentPath + "/" + entry.name,
                          )
                        }
                        title={`Delete ${entry.name}`}
                        aria-label={`Delete ${entry.name}`}
                        className="p-1 hover:text-red-400 text-text-secondary rounded"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {(listing?.entries ?? []).length === 0 && (
                <tr>
                  <td colSpan={3} className="px-4 py-12">
                    <EmptyState
                      icon={Folder}
                      title="This folder is empty"
                      hint="Upload files with the button above, or create a new folder to get started."
                    />
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        )}
      </div>

      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget)}
        title="Delete file"
        description={`Delete "${deleteTarget?.split("/").pop()}"? This cannot be undone.`}
        confirmLabel="Delete"
        variant="destructive"
        loading={deleteMutation.isPending}
      />
    </div>
  );
}
