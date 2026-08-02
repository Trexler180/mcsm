import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Folder, FolderPlus, Pencil, Trash2, X } from "lucide-react";
import { clsx } from "clsx";
import { Button } from "@/components/ui/button";
import { ConfirmDialog, Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { EmptyState } from "@/components/ui/empty-state";
import { api } from "@/lib/api";
import { FOLDER_COLORS, folderAccent } from "@/lib/folders";
import { useNotifications } from "@/store/notifications";
import type { FolderColor, Server, ServerFolder } from "@/lib/types";

/** Shared query key so every folder mutation refreshes the same cache entry. */
export const FOLDERS_KEY = ["server-folders"];

/** Loads the folders the current user can see. */
export function useServerFolders() {
  return useQuery({
    queryKey: FOLDERS_KEY,
    queryFn: () => api.serverFolders.list(),
  });
}

/** The swatch row used by both the create and rename forms. */
function ColorPicker({
  value,
  onChange,
}: {
  value: FolderColor;
  onChange: (c: FolderColor) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {FOLDER_COLORS.map((c) => (
        <button
          key={c || "default"}
          type="button"
          onClick={() => onChange(c)}
          aria-label={c ? `${c} folder color` : "Default folder color"}
          aria-pressed={value === c}
          className={clsx(
            "h-6 w-6 rounded-full border transition-transform",
            folderAccent(c).dot,
            value === c
              ? "scale-110 border-text-primary"
              : "border-transparent hover:scale-105",
          )}
        />
      ))}
    </div>
  );
}

/**
 * A `<select>` over folders plus the ungrouped option. Emits null for
 * ungrouped, matching the API's folder_id contract.
 */
export function FolderSelect({
  value,
  onChange,
  folders,
  id,
  disabled,
}: {
  value: string | null;
  onChange: (folderId: string | null) => void;
  folders: ServerFolder[];
  id?: string;
  disabled?: boolean;
}) {
  return (
    <select
      id={id}
      disabled={disabled}
      className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-50"
      value={value ?? ""}
      onChange={(e) => onChange(e.target.value || null)}
    >
      <option value="">No folder</option>
      {folders.map((f) => (
        <option key={f.id} value={f.id}>
          {f.name}
        </option>
      ))}
    </select>
  );
}

/** One row of the manage dialog: read-only until you click the pencil. */
function FolderRow({
  folder,
  onDelete,
}: {
  folder: ServerFolder;
  onDelete: (f: ServerFolder) => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(folder.name);
  const [description, setDescription] = useState(folder.description);
  const [color, setColor] = useState<FolderColor>(folder.color);

  // Re-sync when a refetch brings new values in while the row sits idle.
  useEffect(() => {
    if (editing) return;
    setName(folder.name);
    setDescription(folder.description);
    setColor(folder.color);
  }, [editing, folder.name, folder.description, folder.color]);

  const save = useMutation({
    mutationFn: () =>
      api.serverFolders.update(folder.id, {
        name: name.trim(),
        description: description.trim(),
        color,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: FOLDERS_KEY });
      setEditing(false);
      success("Folder updated");
    },
    onError: (e: Error) => error("Could not update folder", e.message),
  });

  const accent = folderAccent(folder.color);

  if (!editing) {
    return (
      <div className="flex items-center gap-2.5 rounded-md border border-border bg-surface-2/40 px-3 py-2">
        <span className={clsx("h-2.5 w-2.5 flex-shrink-0 rounded-full", accent.dot)} />
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium text-text-primary">
            {folder.name}
          </p>
          {folder.description && (
            <p className="truncate text-xs text-text-secondary">
              {folder.description}
            </p>
          )}
        </div>
        <span className="flex-shrink-0 text-xs tabular-nums text-text-secondary">
          {folder.server_count}
        </span>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => setEditing(true)}
          title="Rename folder"
          aria-label={`Rename ${folder.name}`}
        >
          <Pencil className="h-3.5 w-3.5" />
        </Button>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => onDelete(folder)}
          title="Delete folder"
          aria-label={`Delete ${folder.name}`}
        >
          <Trash2 className="h-3.5 w-3.5 text-red-400" />
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-2.5 rounded-md border border-accent/30 bg-surface-2/40 px-3 py-2.5">
      <Input
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder="Folder name"
        maxLength={60}
      />
      <Input
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        placeholder="Optional description…"
        maxLength={200}
      />
      <div className="flex items-center justify-between gap-2">
        <ColorPicker value={color} onChange={setColor} />
        <div className="flex flex-shrink-0 gap-1.5">
          <Button
            size="sm"
            variant="ghost"
            onClick={() => setEditing(false)}
            aria-label="Cancel rename"
          >
            <X className="h-3.5 w-3.5" />
          </Button>
          <Button
            size="sm"
            onClick={() => save.mutate()}
            loading={save.isPending}
            disabled={!name.trim()}
            aria-label="Save folder"
          >
            <Check className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>
    </div>
  );
}

/**
 * Admin-only folder management: create, rename, recolor, delete. Deleting a
 * folder never touches the servers in it — they become ungrouped — so the
 * confirmation says exactly that instead of a generic destructive warning.
 */
export function ManageFoldersDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const { data: folders = [] } = useServerFolders();

  const [name, setName] = useState("");
  const [color, setColor] = useState<FolderColor>("");
  const [pendingDelete, setPendingDelete] = useState<ServerFolder | null>(null);

  const create = useMutation({
    mutationFn: () => api.serverFolders.create({ name: name.trim(), color }),
    onSuccess: (folder) => {
      qc.invalidateQueries({ queryKey: FOLDERS_KEY });
      setName("");
      setColor("");
      success(`Folder "${folder.name}" created`);
    },
    onError: (e: Error) => error("Could not create folder", e.message),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.serverFolders.delete(id),
    onSuccess: () => {
      // Servers keep existing but lose their folder, so refresh both lists.
      qc.invalidateQueries({ queryKey: FOLDERS_KEY });
      qc.invalidateQueries({ queryKey: ["servers"] });
      qc.invalidateQueries({ queryKey: ["overview"] });
      setPendingDelete(null);
      success("Folder deleted", "Its servers are still here, just ungrouped.");
    },
    onError: (e: Error) => error("Could not delete folder", e.message),
  });

  return (
    <>
      <Dialog
        open={open}
        onClose={onClose}
        title="Server folders"
        titleIcon={<Folder className="h-5 w-5 text-accent" />}
        description="Group related servers together. Deleting a folder keeps its servers."
        className="max-w-lg"
      >
        <div className="space-y-4">
          {folders.length === 0 ? (
            <EmptyState
              icon={Folder}
              title="No folders yet"
              hint="Create one below, then assign servers to it from the server list or its options tab."
              className="py-6"
            />
          ) : (
            <div className="space-y-2">
              {folders.map((f) => (
                <FolderRow key={f.id} folder={f} onDelete={setPendingDelete} />
              ))}
            </div>
          )}

          <div className="space-y-2.5 border-t border-border pt-4">
            <Label htmlFor="new-folder-name">New folder</Label>
            <Input
              id="new-folder-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && name.trim()) create.mutate();
              }}
              placeholder="Minigames"
              maxLength={60}
            />
            <div className="flex items-center justify-between gap-2">
              <ColorPicker value={color} onChange={setColor} />
              <Button
                size="sm"
                onClick={() => create.mutate()}
                loading={create.isPending}
                disabled={!name.trim()}
                className="flex-shrink-0"
              >
                <FolderPlus className="h-3.5 w-3.5" /> Add
              </Button>
            </div>
          </div>
        </div>
      </Dialog>

      <ConfirmDialog
        open={!!pendingDelete}
        onClose={() => setPendingDelete(null)}
        onConfirm={() => pendingDelete && remove.mutate(pendingDelete.id)}
        title="Delete folder"
        description={
          pendingDelete
            ? `Delete "${pendingDelete.name}"? Its ${pendingDelete.server_count} server${
                pendingDelete.server_count === 1 ? "" : "s"
              } will move to Ungrouped — nothing is deleted or stopped.`
            : ""
        }
        confirmLabel="Delete folder"
        variant="destructive"
        loading={remove.isPending}
      />
    </>
  );
}

/** Moves a single server into (or out of) a folder from the server list. */
export function MoveToFolderDialog({
  server,
  folders,
  onClose,
}: {
  server: Server | null;
  folders: ServerFolder[];
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [target, setTarget] = useState<string | null>(null);

  // Start from wherever the server currently lives each time it's opened.
  useEffect(() => {
    setTarget(server?.folder_id ?? null);
  }, [server?.id, server?.folder_id]);

  const move = useMutation({
    mutationFn: () =>
      api.servers.update(server!.id, { folder_id: target }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      qc.invalidateQueries({ queryKey: FOLDERS_KEY });
      qc.invalidateQueries({ queryKey: ["overview"] });
      if (server) qc.invalidateQueries({ queryKey: ["server", server.id] });
      const name = folders.find((f) => f.id === target)?.name;
      success(name ? `Moved to ${name}` : "Removed from folder");
      onClose();
    },
    onError: (e: Error) => error("Could not move server", e.message),
  });

  return (
    <Dialog
      open={!!server}
      onClose={onClose}
      title="Move to folder"
      titleIcon={<Folder className="h-5 w-5 text-accent" />}
      description={server ? server.name : undefined}
    >
      <div className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="move-folder">Folder</Label>
          <FolderSelect
            id="move-folder"
            value={target}
            onChange={setTarget}
            folders={folders}
          />
        </div>
        <div className="flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={() => move.mutate()}
            loading={move.isPending}
            disabled={(server?.folder_id ?? null) === target}
          >
            Move
          </Button>
        </div>
      </div>
    </Dialog>
  );
}
