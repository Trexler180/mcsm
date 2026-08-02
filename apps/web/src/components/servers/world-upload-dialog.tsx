import { useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, FileArchive, Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import {
  defaultServerProperties,
  parseProperties,
  serializeProperties,
} from "./options/properties-schema";

// Characters the agent rejects in a world folder name, mirrored here so the
// dialog can say so before spending an upload on it. Spaces stay legal —
// plenty of downloaded maps have them in the folder name.
const INVALID_NAME_CHARS = /[/\\:*?"<>|]|\p{Cc}/u;
const INVALID_NAME_CHARS_G = /[/\\:*?"<>|]|\p{Cc}/gu;

function formatSize(bytes: number): string {
  const units = ["B", "KB", "MB", "GB"];
  let i = 0;
  let v = bytes;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

// Derive a sensible folder name from the archive's file name: "My World.zip"
// and "My World (1).zip" both want to land in a folder called "My World".
function nameFromFile(fileName: string): string {
  return fileName
    .replace(/\.zip$/i, "")
    .replace(/\s*\(\d+\)$/, "")
    .replace(INVALID_NAME_CHARS_G, " ")
    .trim()
    .slice(0, 64);
}

function nameError(name: string): string | null {
  const n = name.trim();
  if (!n) return "Give the world a folder name.";
  if (n.length > 64) return "Keep the name to 64 characters or fewer.";
  if (n.startsWith(".")) return "The name can't start with a dot.";
  if (n.endsWith(".")) return "The name can't end with a dot.";
  if (INVALID_NAME_CHARS.test(n))
    return 'The name can\'t contain / \\ : * ? " < > or |.';
  return null;
}

export function WorldUploadDialog({
  serverId,
  existingWorlds,
  existingFolders,
  activeWorld,
  serverRunning,
  onClose,
}: {
  serverId: string;
  /** Folders in the server root that are worlds. */
  existingWorlds: string[];
  /** Every folder in the server root — landing on "mods" is as destructive
   *  as landing on a world, so the whole set has to be checked. */
  existingFolders: string[];
  /** Folder that server.properties points level-name at. */
  activeWorld: string;
  serverRunning: boolean;
  onClose: () => void;
}) {
  const [file, setFile] = useState<File | null>(null);
  const [name, setName] = useState("");
  const [confirmReplace, setConfirmReplace] = useState(false);
  const [makeActive, setMakeActive] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [progress, setProgress] = useState<number | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const qc = useQueryClient();
  const { success, error } = useNotifications();

  const trimmed = name.trim();
  const conflict = useMemo(
    () =>
      existingFolders.some((f) => f.toLowerCase() === trimmed.toLowerCase()),
    [existingFolders, trimmed],
  );
  // Replacing a world is routine; replacing mods/ or config/ is someone
  // mistyping a name, and deserves a louder warning.
  const conflictIsWorld = useMemo(
    () => existingWorlds.some((w) => w.toLowerCase() === trimmed.toLowerCase()),
    [existingWorlds, trimmed],
  );
  const isActiveWorld =
    !!trimmed && trimmed.toLowerCase() === activeWorld.toLowerCase();
  // The agent refuses this too; catching it here saves uploading gigabytes
  // only to be told the server has that world open.
  const blockedByRunningServer = serverRunning && isActiveWorld;
  const invalid = file ? nameError(name) : null;

  const chooseFile = (picked: File | null) => {
    if (!picked) return;
    if (!picked.name.toLowerCase().endsWith(".zip")) {
      error("Worlds must be uploaded as a .zip file");
      return;
    }
    setFile(picked);
    setName((current) => current || nameFromFile(picked.name));
    setConfirmReplace(false);
  };

  const uploadMutation = useMutation({
    mutationFn: async () => {
      if (!file) throw new Error("Choose a world zip first.");
      setProgress(0);
      const result = await api.worlds.upload(
        serverId,
        file,
        { name: trimmed, overwrite: conflict },
        (p) =>
          setProgress(
            p.total > 0
              ? Math.min(100, Math.round((p.loaded / p.total) * 100))
              : 0,
          ),
      );
      // Once the bytes are sent the request is no longer uploading, it's
      // unpacking on the node — for a large world the longer half of the wait.
      setProgress(null);

      if (makeActive) {
        const properties = await api.files
          .readContent(serverId, "/server.properties")
          .catch(() => defaultServerProperties);
        await api.files.writeContent(
          serverId,
          "/server.properties",
          serializeProperties(properties, {
            ...parseProperties(properties),
            "level-name": result.name,
          }),
        );
      }
      return result;
    },
    onSuccess: (result) => {
      qc.invalidateQueries({ queryKey: ["files", serverId] });
      qc.invalidateQueries({ queryKey: ["file-tree", serverId] });
      qc.invalidateQueries({
        queryKey: ["file-content", serverId, "/server.properties"],
      });
      success(
        result.replaced
          ? `Replaced world "${result.name}"`
          : `Uploaded world "${result.name}"`,
        makeActive
          ? "It is now the active world — restart the server to load it."
          : `${result.files.toLocaleString()} files, ${formatSize(result.bytes)}.`,
      );
      onClose();
    },
    onError: (e: Error) => error("World upload failed", e.message),
    onSettled: () => setProgress(null),
  });

  const busy = uploadMutation.isPending;
  const canSubmit =
    !!file &&
    !invalid &&
    !blockedByRunningServer &&
    (!conflict || confirmReplace) &&
    !busy;

  return (
    <Dialog
      open
      // Closing mid-upload would orphan a half-written world, so the dialog
      // stays put until the request settles.
      onClose={busy ? () => {} : onClose}
      title="Upload world"
      description="Install a zipped world folder into this server."
      titleIcon={<Upload className="h-5 w-5 flex-shrink-0 text-accent" />}
    >
      <input
        ref={inputRef}
        type="file"
        accept=".zip,application/zip"
        className="hidden"
        onChange={(e) => {
          const picked = e.target.files?.[0] ?? null;
          e.target.value = "";
          chooseFile(picked);
        }}
      />

      <button
        type="button"
        disabled={busy}
        onClick={() => inputRef.current?.click()}
        onDragOver={(e) => {
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(e) => {
          e.preventDefault();
          setDragging(false);
          if (!busy) chooseFile(e.dataTransfer.files?.[0] ?? null);
        }}
        className={`flex w-full flex-col items-center gap-2 rounded-md border border-dashed px-4 py-6 text-center transition-colors disabled:opacity-50 ${
          dragging
            ? "border-accent bg-accent/5"
            : "border-border hover:border-border-hover hover:bg-surface-2/40"
        }`}
      >
        <FileArchive className="h-6 w-6 text-text-secondary" />
        {file ? (
          <>
            <span className="max-w-full truncate text-sm font-medium text-text-primary">
              {file.name}
            </span>
            <span className="text-xs text-text-secondary">
              {formatSize(file.size)} — click to choose a different file
            </span>
          </>
        ) : (
          <>
            <span className="text-sm font-medium text-text-primary">
              Choose a .zip file, or drop one here
            </span>
            <span className="text-xs text-text-secondary">
              The world folder, a folder wrapping it, or a whole server backup —
              whatever holds level.dat is what gets installed.
            </span>
          </>
        )}
      </button>

      {file && (
        <div className="mt-4 space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="world-name">Folder name</Label>
            <Input
              id="world-name"
              value={name}
              disabled={busy}
              onChange={(e) => {
                setName(e.target.value);
                setConfirmReplace(false);
              }}
              placeholder="world"
            />
            <p className="text-xs text-text-secondary">
              Created as <span className="font-mono">/{trimmed || "…"}</span> in
              the server directory.
            </p>
          </div>

          {invalid && <p className="text-xs text-red-400">{invalid}</p>}

          {!invalid && blockedByRunningServer && (
            <div className="flex gap-2 rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2">
              <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-red-400" />
              <p className="text-xs text-red-300">
                The server is running with{" "}
                <span className="font-mono">{activeWorld}</span> open. Stop it
                first, or upload under a different name.
              </p>
            </div>
          )}

          {!invalid && !blockedByRunningServer && conflict && (
            <div className="rounded-md border border-yellow-500/30 bg-yellow-500/10 px-3 py-2">
              <div className="flex gap-2">
                <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-yellow-400" />
                <p className="text-xs text-yellow-200">
                  {conflictIsWorld ? (
                    <>
                      A world called{" "}
                      <span className="font-mono">{trimmed}</span> already
                      exists. Uploading replaces it entirely — back it up first
                      if you want to keep it.
                    </>
                  ) : (
                    <>
                      <span className="font-mono">{trimmed}</span> is an
                      existing folder in the server directory and isn't a world.
                      Uploading deletes it and everything in it — check the name
                      before continuing.
                    </>
                  )}
                </p>
              </div>
              <label className="mt-2 flex items-center gap-2 pl-6 text-xs text-yellow-200">
                <input
                  type="checkbox"
                  checked={confirmReplace}
                  disabled={busy}
                  onChange={(e) => setConfirmReplace(e.target.checked)}
                  className="accent-yellow-500"
                />
                {conflictIsWorld
                  ? "Replace the existing world"
                  : `Delete the existing ${trimmed} folder`}
              </label>
            </div>
          )}

          {!invalid && !isActiveWorld && (
            <label className="flex items-start gap-2 text-sm text-text-primary">
              <input
                type="checkbox"
                checked={makeActive}
                disabled={busy}
                onChange={(e) => setMakeActive(e.target.checked)}
                className="mt-0.5 accent-accent"
              />
              <span>
                Make this the active world
                <span className="block text-xs text-text-secondary">
                  Points <span className="font-mono">level-name</span> at it.
                  {serverRunning
                    ? " Takes effect on the next restart."
                    : " Loaded the next time the server starts."}
                </span>
              </span>
            </label>
          )}
        </div>
      )}

      {busy && (
        <div className="mt-4">
          <div className="mb-1 flex items-center justify-between text-xs text-text-secondary">
            <span>
              {progress === null ? "Unpacking on the server…" : "Uploading…"}
            </span>
            {progress !== null && <span>{progress}%</span>}
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface-2">
            <div
              className={`h-full rounded-full bg-accent ${
                progress === null ? "w-full animate-pulse" : "transition-all"
              }`}
              style={progress === null ? undefined : { width: `${progress}%` }}
            />
          </div>
        </div>
      )}

      <div className="mt-6 flex justify-end gap-3">
        <Button variant="outline" onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button
          onClick={() => uploadMutation.mutate()}
          disabled={!canSubmit}
          loading={busy}
        >
          {!busy && <Upload className="h-3.5 w-3.5" />}
          {conflict ? "Replace world" : "Upload"}
        </Button>
      </div>
    </Dialog>
  );
}
