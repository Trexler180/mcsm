import { useRef } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Image as ImageIcon,

  Trash2,
  Upload,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { Server } from "@/lib/types";
import { Panel } from "../shared";
import { parseProperties } from "./properties-schema";


const SERVER_ICON_PATH = "/server-icon.png";

function blobToDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as string);
    reader.onerror = () => reject(new Error("Failed to read image"));
    reader.readAsDataURL(blob);
  });
}

// Center-crop to a square and scale to 64x64, then encode as PNG. Returns the
// raw bytes ready to write to server-icon.png.
async function imageFileToServerIcon(file: File): Promise<Uint8Array> {
  const bitmap = await createImageBitmap(file);
  try {
    const canvas = document.createElement("canvas");
    canvas.width = 64;
    canvas.height = 64;
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("Canvas is not supported in this browser");
    const side = Math.min(bitmap.width, bitmap.height);
    const sx = (bitmap.width - side) / 2;
    const sy = (bitmap.height - side) / 2;
    ctx.imageSmoothingEnabled = true;
    ctx.imageSmoothingQuality = "high";
    ctx.drawImage(bitmap, sx, sy, side, side, 0, 0, 64, 64);
    const blob = await new Promise<Blob | null>((resolve) =>
      canvas.toBlob(resolve, "image/png"),
    );
    if (!blob) throw new Error("Failed to encode the icon as PNG");
    return new Uint8Array(await blob.arrayBuffer());
  } finally {
    bitmap.close();
  }
}

function IconPreview({
  src,
  loading,
  className,
}: {
  src?: string;
  loading?: boolean;
  className?: string;
}) {
  return (
    <div
      className={`flex flex-shrink-0 items-center justify-center overflow-hidden rounded-md border border-border bg-surface-2 ${
        className ?? "h-16 w-16"
      }`}
    >
      {loading ? (
        <div className="h-4 w-4 animate-spin rounded-full border-2 border-accent border-t-transparent" />
      ) : src ? (
        <img
          src={src}
          alt="Server icon"
          className="h-full w-full [image-rendering:pixelated]"
        />
      ) : (
        <ImageIcon className="h-6 w-6 text-text-secondary" />
      )}
    </div>
  );
}

export function ServerIconOptionsPanel({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const fileInputRef = useRef<HTMLInputElement>(null);

  const iconQuery = useQuery({
    queryKey: ["server-icon", server.id],
    queryFn: async () => {
      const bytes = await api.files.readBytes(server.id, SERVER_ICON_PATH);
      return blobToDataUrl(new Blob([bytes as BlobPart], { type: "image/png" }));
    },
    retry: false,
    staleTime: 0,
  });
  const hasIcon = !!iconQuery.data;

  // The active world is whatever level-name points at (default "world"); its
  // map render lives at <world>/icon.png. We surface it as a one-click source so
  // users can adopt the auto-generated world icon instead of uploading one.
  const levelNameQuery = useQuery({
    queryKey: ["file-content", server.id, "/server.properties"],
    queryFn: () => api.files.readContent(server.id, "/server.properties"),
    retry: false,
  });
  const worldName =
    parseProperties(levelNameQuery.data ?? "")["level-name"]?.trim() || "world";
  const worldIconPath = `/${worldName}/icon.png`;

  const worldIconQuery = useQuery({
    queryKey: ["world-icon", server.id, worldIconPath],
    queryFn: async () => {
      const bytes = await api.files.readBytes(server.id, worldIconPath);
      return blobToDataUrl(new Blob([bytes as BlobPart], { type: "image/png" }));
    },
    retry: false,
    staleTime: 0,
  });
  const hasWorldIcon = !!worldIconQuery.data;

  const restartHint =
    server.status === "online"
      ? "Restart the Minecraft server to refresh the icon players see."
      : undefined;

  const uploadMutation = useMutation({
    mutationFn: async (file: File) => {
      if (!file.type.startsWith("image/")) {
        throw new Error("Choose an image file (PNG, JPG, GIF, …).");
      }
      const bytes = await imageFileToServerIcon(file);
      await api.files.writeBytes(server.id, SERVER_ICON_PATH, bytes);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server-icon", server.id] });
      success("Server icon updated", restartHint);
    },
    onError: (e: Error) => error("Upload failed", e.message),
  });

  // Copy the world's icon.png onto server-icon.png. The world icon is already a
  // valid 64×64 PNG, so we copy the bytes verbatim rather than re-encoding.
  const useWorldIconMutation = useMutation({
    mutationFn: async () => {
      const bytes = await api.files.readBytes(server.id, worldIconPath);
      await api.files.writeBytes(server.id, SERVER_ICON_PATH, bytes);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server-icon", server.id] });
      success("Using world icon", restartHint);
    },
    onError: (e: Error) => error("Failed to apply world icon", e.message),
  });

  const removeMutation = useMutation({
    mutationFn: () => api.files.delete(server.id, SERVER_ICON_PATH),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server-icon", server.id] });
      success("Server icon removed", restartHint);
    },
    onError: (e: Error) => error("Remove failed", e.message),
  });

  const busy =
    uploadMutation.isPending ||
    useWorldIconMutation.isPending ||
    removeMutation.isPending;

  return (
    <Panel
      title="Server Icon"
      description="The 64×64 icon shown next to your server in the multiplayer list. Use the world's own icon or upload a custom image."
      actions={
        hasIcon ? (
          <Button
            variant="outline"
            size="sm"
            onClick={() => removeMutation.mutate()}
            loading={removeMutation.isPending}
            disabled={busy}
          >
            {!removeMutation.isPending && <Trash2 className="h-3.5 w-3.5" />}
            Remove
          </Button>
        ) : undefined
      }
    >
      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        className="hidden"
        onChange={(e) => {
          const file = e.target.files?.[0];
          e.target.value = "";
          if (file) uploadMutation.mutate(file);
        }}
      />

      <div className="space-y-5">
        {/* Current icon */}
        <div className="flex items-center gap-4">
          <IconPreview src={iconQuery.data} loading={iconQuery.isLoading} />
          <div className="min-w-0">
            <p className="text-sm font-medium text-text-primary">Current icon</p>
            <p className="text-sm text-text-secondary">
              {hasIcon
                ? "Saved as server-icon.png in the server root."
                : "No server icon set yet — pick a source below."}
            </p>
          </div>
        </div>

        {/* Sources */}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          {/* World icon */}
          <div className="flex flex-col gap-3 rounded-md border border-border bg-surface-2/40 p-4">
            <div className="flex items-center gap-3">
              <IconPreview
                src={worldIconQuery.data}
                loading={worldIconQuery.isLoading}
                className="h-12 w-12"
              />
              <div className="min-w-0">
                <p className="text-sm font-medium text-text-primary">
                  World icon
                </p>
                <p className="truncate text-xs text-text-secondary">
                  {hasWorldIcon
                    ? `From ${worldName}/icon.png`
                    : `No icon.png in ${worldName}`}
                </p>
              </div>
            </div>
            <Button
              variant="outline"
              size="sm"
              className="w-full"
              onClick={() => useWorldIconMutation.mutate()}
              loading={useWorldIconMutation.isPending}
              disabled={!hasWorldIcon || busy}
            >
              Use world icon
            </Button>
          </div>

          {/* Custom upload */}
          <div className="flex flex-col gap-3 rounded-md border border-border bg-surface-2/40 p-4">
            <div className="flex items-center gap-3">
              <IconPreview className="h-12 w-12" />
              <div className="min-w-0">
                <p className="text-sm font-medium text-text-primary">
                  Custom image
                </p>
                <p className="truncate text-xs text-text-secondary">
                  Any image, auto-cropped to 64×64
                </p>
              </div>
            </div>
            <Button
              variant="outline"
              size="sm"
              className="w-full"
              onClick={() => fileInputRef.current?.click()}
              loading={uploadMutation.isPending}
              disabled={busy}
            >
              {!uploadMutation.isPending && <Upload className="h-3.5 w-3.5" />}
              {hasIcon ? "Upload new" : "Upload"}
            </Button>
          </div>
        </div>
      </div>
    </Panel>
  );
}

// A small accented section heading — an icon chip plus an uppercase label — so
