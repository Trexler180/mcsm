import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  Copy,
  Folder,
  FolderTree,

  MemoryStick,
  Network,
  Save,
  Server as ServerIcon,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { ConfirmDialog, Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import {
  FolderSelect,
  useServerFolders,
} from "@/components/servers/folder-dialogs";
import { useNotifications } from "@/store/notifications";
import type { Server } from "@/lib/types";


export function PanelOptionsPanel({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const navigate = useNavigate();
  const [showDelete, setShowDelete] = useState(false);
  const [purgeFiles, setPurgeFiles] = useState(false);
  const [purgeBackups, setPurgeBackups] = useState(false);

  const [showClone, setShowClone] = useState(false);
  const [cloneName, setCloneName] = useState("");
  const [cloneDir, setCloneDir] = useState("");
  const [cloneMods, setCloneMods] = useState(true);

  const { data: folders = [] } = useServerFolders();
  const [folderId, setFolderId] = useState<string | null>(server.folder_id);

  const [form, setForm] = useState({
    name: server.name,
    description: server.description ?? "",
    directory_path: server.directory_path,
    port: String(server.port),
    ram_mb_max: String(server.ram_mb_max),
    ram_mb_min: String(server.ram_mb_min),
  });

  const updateMutation = useMutation({
    mutationFn: () =>
      api.servers.update(server.id, {
        name: form.name,
        description: form.description || null,
        directory_path: form.directory_path,
        port: Number(form.port),
        ram_mb_max: Number(form.ram_mb_max),
        ram_mb_min: Number(form.ram_mb_min),
        folder_id: folderId,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      // Moving the server changes both folders' counts.
      qc.invalidateQueries({ queryKey: ["server-folders"] });
      success("Settings saved");
    },
    onError: (e: Error) => error("Save failed", e.message),
  });

  const deleteMutation = useMutation({
    mutationFn: () =>
      api.servers.delete(server.id, {
        files: purgeFiles,
        backups: purgeBackups,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      navigate({ to: "/servers" });
    },
    onError: (e: Error) => error("Delete failed", e.message),
  });

  const openDelete = () => {
    setPurgeFiles(false);
    setPurgeBackups(false);
    setShowDelete(true);
  };

  const cloneMutation = useMutation({
    mutationFn: () =>
      api.servers.clone(server.id, {
        name: cloneName.trim(),
        directory_path: cloneDir.trim() || undefined,
        copy_mods: cloneMods,
      }),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      setShowClone(false);
      const modNote =
        res.mods_queued > 0
          ? `Installing ${res.mods_queued} mod${res.mods_queued === 1 ? "" : "s"} in the background.`
          : "";
      const skipNote =
        res.mods_skipped > 0
          ? ` ${res.mods_skipped} custom jar${res.mods_skipped === 1 ? "" : "s"} skipped (re-upload manually).`
          : "";
      success("Server cloned", (modNote + skipNote).trim() || undefined);
      navigate({
        to: "/servers/$id/$section",
        params: { id: res.server.id, section: "dashboard" },
      });
    },
    onError: (e: Error) => error("Clone failed", e.message),
  });

  const openClone = () => {
    setCloneName(`${server.name} (copy)`);
    setCloneDir("");
    setCloneMods(true);
    setShowClone(true);
  };

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }));

  // Drive the footer's state copy + Save button so the panel reacts to edits
  // instead of offering an always-on, always-identical "Save Changes".
  const dirty =
    form.name !== server.name ||
    form.description !== (server.description ?? "") ||
    form.directory_path !== server.directory_path ||
    form.port !== String(server.port) ||
    form.ram_mb_max !== String(server.ram_mb_max) ||
    form.ram_mb_min !== String(server.ram_mb_min) ||
    folderId !== server.folder_id;

  const inputLife = "hover:border-border-hover";

  return (
    <>
      <section className="overflow-hidden rounded-lg border border-border bg-surface">
        {/* Header — a small accent icon chip gives the card a face without
            eating a full title bar. */}
        <div className="flex items-center gap-2.5 border-b border-border bg-surface-2/40 px-3.5 py-2.5">
          <div className="grid h-7 w-7 flex-shrink-0 place-items-center rounded-md border border-accent/20 bg-accent/10 text-accent">
            <ServerIcon className="h-4 w-4" />
          </div>
          <h2 className="text-sm font-semibold text-text-primary">
            Panel Options
          </h2>
          <span className="truncate text-xs text-text-secondary">
            — name, location & resources
          </span>
        </div>

        <div className="space-y-3.5 p-3.5">
          {/* Identity */}
          <div className="space-y-2.5">
            <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
              <div className="space-y-1">
                <Label>Display name</Label>
                <Input
                  value={form.name}
                  onChange={f("name")}
                  className={inputLife}
                  placeholder="My Survival Server"
                />
              </div>
              <div className="space-y-1">
                <Label>Description</Label>
                <Input
                  value={form.description}
                  onChange={f("description")}
                  placeholder="Optional note…"
                  className={inputLife}
                />
              </div>
            </div>
            <div className="space-y-1">
              <Label className="flex items-center gap-1.5">
                <FolderTree className="h-3.5 w-3.5 text-text-secondary" />
                Server directory
              </Label>
              <Input
                value={form.directory_path}
                onChange={f("directory_path")}
                className={`font-mono ${inputLife}`}
                placeholder="E:/mc-test"
              />
            </div>
            {/* Panel-side grouping only — this has no bearing on where the
                server's files live on the node. */}
            <div className="space-y-1">
              <Label className="flex items-center gap-1.5" htmlFor="server-folder">
                <Folder className="h-3.5 w-3.5 text-text-secondary" />
                Folder
              </Label>
              <FolderSelect
                id="server-folder"
                value={folderId}
                onChange={setFolderId}
                folders={folders}
              />
            </div>
          </div>

          {/* Resources */}
          <div className="space-y-2.5">
            <div className="grid grid-cols-3 gap-2.5">
              <div className="space-y-1">
                <Label className="flex items-center gap-1.5">
                  <Network className="h-3.5 w-3.5 text-text-secondary" />
                  Port
                </Label>
                <Input
                  type="number"
                  value={form.port}
                  onChange={f("port")}
                  className={inputLife}
                />
              </div>
              <div className="space-y-1">
                <Label className="flex items-center gap-1.5">
                  <MemoryStick className="h-3.5 w-3.5 text-text-secondary" />
                  Max RAM
                </Label>
                <div className="relative">
                  <Input
                    type="number"
                    value={form.ram_mb_max}
                    onChange={f("ram_mb_max")}
                    className={`pr-10 ${inputLife}`}
                  />
                  <span className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-xs text-text-secondary">
                    MB
                  </span>
                </div>
              </div>
              <div className="space-y-1">
                <Label className="flex items-center gap-1.5">
                  <MemoryStick className="h-3.5 w-3.5 text-text-secondary" />
                  Min RAM
                </Label>
                <div className="relative">
                  <Input
                    type="number"
                    value={form.ram_mb_min}
                    onChange={f("ram_mb_min")}
                    className={`pr-10 ${inputLife}`}
                  />
                  <span className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-xs text-text-secondary">
                    MB
                  </span>
                </div>
              </div>
            </div>
          </div>
        </div>

        {/* Footer save bar — reflects whether there's anything to save. */}
        <div className="flex items-center justify-between gap-3 border-t border-border bg-surface-2/30 px-3.5 py-2">
          <span className="text-xs text-text-secondary">
            {dirty ? "Unsaved changes" : "All changes saved"}
          </span>
          <Button
            size="sm"
            onClick={() => updateMutation.mutate()}
            loading={updateMutation.isPending}
            disabled={!dirty}
          >
            {!updateMutation.isPending && <Save className="h-3.5 w-3.5" />}
            {dirty ? "Save" : "Saved"}
          </Button>
        </div>
      </section>

      {/* Clone — reuse this server's platform/version/config as the starting
          point for a new one. */}
      <div className="flex items-center gap-3 rounded-lg border border-border bg-surface px-3.5 py-2.5">
        <Copy className="h-4 w-4 flex-shrink-0 text-accent" />
        <div className="min-w-0 flex-1">
          <span className="text-sm font-medium text-text-primary">
            Clone server
          </span>
          <span className="ml-2 text-xs text-text-secondary">
            New server with this one's config and mods — not its world.
          </span>
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={openClone}
          className="flex-shrink-0"
        >
          <Copy className="h-3.5 w-3.5" /> Clone
        </Button>
      </div>

      {/* Danger zone — a compact red-tinted strip so it reads as its own place
          without claiming a full card. */}
      <div className="flex items-center gap-3 rounded-lg border border-red-900/40 bg-red-950/10 px-3.5 py-2.5">
        <AlertTriangle className="h-4 w-4 flex-shrink-0 text-red-400" />
        <div className="min-w-0 flex-1">
          <span className="text-sm font-medium text-red-400">Delete server</span>
          <span className="ml-2 text-xs text-text-secondary">
            Files on the node are kept unless you wipe them.
          </span>
        </div>
        <Button
          variant="destructive"
          size="sm"
          onClick={openDelete}
          className="flex-shrink-0"
        >
          <Trash2 className="h-3.5 w-3.5" /> Delete
        </Button>
      </div>

      <Dialog
        open={showClone}
        onClose={() => setShowClone(false)}
        title="Clone server"
        titleIcon={<Copy className="h-5 w-5 text-accent" />}
        description={`Creates a new ${server.platform} ${server.mc_version} server with the same Java, memory, and mod list. The world and player data are not copied.`}
      >
        <div className="space-y-3">
          <div className="space-y-1">
            <Label>New server name</Label>
            <Input
              value={cloneName}
              onChange={(e) => setCloneName(e.target.value)}
              placeholder="My Survival Server (copy)"
            />
          </div>
          <div className="space-y-1">
            <Label>Directory (optional)</Label>
            <Input
              value={cloneDir}
              onChange={(e) => setCloneDir(e.target.value)}
              className="font-mono"
              placeholder="Leave blank to auto-name from the server name"
            />
          </div>
          <label className="flex items-center gap-2 text-sm text-text-secondary cursor-pointer">
            <input
              type="checkbox"
              checked={cloneMods}
              onChange={(e) => setCloneMods(e.target.checked)}
            />
            Re-install the mod list (source-tracked mods; custom jars skipped)
          </label>
          <div className="flex justify-end gap-2 pt-1">
            <Button variant="outline" size="sm" onClick={() => setShowClone(false)}>
              Cancel
            </Button>
            <Button
              size="sm"
              onClick={() => cloneMutation.mutate()}
              loading={cloneMutation.isPending}
              disabled={!cloneName.trim()}
            >
              <Copy className="h-3.5 w-3.5" /> Clone
            </Button>
          </div>
        </div>
      </Dialog>

      <ConfirmDialog
        open={showDelete}
        onClose={() => setShowDelete(false)}
        onConfirm={() => deleteMutation.mutate()}
        title="Delete server"
        description={`Delete "${server.name}"? This cannot be undone.`}
        confirmLabel="Delete"
        variant="destructive"
        loading={deleteMutation.isPending}
      >
        <div className="mt-4 space-y-2 rounded-md border border-border bg-surface p-3">
          <label className="flex items-center gap-2 text-sm text-text-secondary cursor-pointer">
            <input
              type="checkbox"
              checked={purgeFiles}
              onChange={(e) => setPurgeFiles(e.target.checked)}
            />
            Also delete the server files on disk
          </label>
          <label className="flex items-center gap-2 text-sm text-text-secondary cursor-pointer">
            <input
              type="checkbox"
              checked={purgeBackups}
              onChange={(e) => setPurgeBackups(e.target.checked)}
            />
            Also delete all backups
          </label>
          {(purgeFiles || purgeBackups) && (
            <p className="text-xs text-red-400">
              Permanently erases the selected data from the node. The node must
              be online or the delete will be cancelled.
            </p>
          )}
        </div>
      </ConfirmDialog>
    </>
  );
}
