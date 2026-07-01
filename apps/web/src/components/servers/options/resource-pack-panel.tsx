import { useEffect, useRef, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Copy,

  Link2,
  Save,

  Upload,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { Server } from "@/lib/types";
import { Panel } from "../shared";
import {
  defaultServerProperties,
  parseProperties,
  serializeProperties,
} from "./properties-schema";


type ResourcePackSettings = {
  enabled?: boolean;
  path?: string;
  public_id?: string;
  sha1?: string;
  file_name?: string;
  source?: "uploaded" | "world";
  required?: boolean;
  prompt?: string;
  updated_at?: string;
};

type WorldResourcePack = {
  path: string;
  worldName: string;
  size: number;
  modified: string;
};

const RESOURCE_PACK_DIR = "/resource-packs";

function getResourcePackSettings(server: Server): ResourcePackSettings {
  const value = server.settings?.resource_pack;
  if (!value || typeof value !== "object") return {};
  return value as ResourcePackSettings;
}

function makePublicId() {
  const browserCrypto = globalThis.crypto as Crypto & {
    randomUUID?: () => string;
  };
  if (browserCrypto.randomUUID) return browserCrypto.randomUUID();
  const bytes = new Uint8Array(16);
  browserCrypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

function resourcePackPathFor(fileName: string) {
  const cleaned = fileName
    .replace(/\\/g, "/")
    .split("/")
    .pop()
    ?.replace(/[^a-zA-Z0-9._-]/g, "-");
  const name =
    cleaned && cleaned.toLowerCase().endsWith(".zip")
      ? cleaned
      : "resource-pack.zip";
  return `${RESOURCE_PACK_DIR}/${name}`;
}

function resourcePackSourceFor(path: string): ResourcePackSettings["source"] {
  return path.toLowerCase().endsWith("/resources.zip") ? "world" : "uploaded";
}

async function sha1Hex(file: File) {
  const digest = await crypto.subtle.digest("SHA-1", await file.arrayBuffer());
  return Array.from(new Uint8Array(digest), (b) =>
    b.toString(16).padStart(2, "0"),
  ).join("");
}

async function sha1Bytes(bytes: Uint8Array) {
  const buffer = new ArrayBuffer(bytes.byteLength);
  new Uint8Array(buffer).set(bytes);
  const digest = await crypto.subtle.digest("SHA-1", buffer);
  return Array.from(new Uint8Array(digest), (b) =>
    b.toString(16).padStart(2, "0"),
  ).join("");
}

export function ResourcePackOptionsPanel({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const autoWorldDefaultRef = useRef<string | null>(null);
  const [uploadPct, setUploadPct] = useState<number | null>(null);
  const initial = getResourcePackSettings(server);
  const [form, setForm] = useState({
    enabled: initial.enabled ?? false,
    path: initial.path ?? "",
    public_id: initial.public_id ?? makePublicId(),
    sha1: initial.sha1 ?? "",
    file_name: initial.file_name ?? "",
    source: initial.source ?? resourcePackSourceFor(initial.path ?? ""),
    required: initial.required ?? false,
    prompt: initial.prompt ?? "",
  });

  useEffect(() => {
    const settings = getResourcePackSettings(server);
    setForm({
      enabled: settings.enabled ?? false,
      path: settings.path ?? "",
      public_id: settings.public_id ?? makePublicId(),
      sha1: settings.sha1 ?? "",
      file_name: settings.file_name ?? "",
      source: settings.source ?? resourcePackSourceFor(settings.path ?? ""),
      required: settings.required ?? false,
      prompt: settings.prompt ?? "",
    });
  }, [server]);

  const filesQuery = useQuery({
    queryKey: ["files", server.id, RESOURCE_PACK_DIR],
    queryFn: () => api.files.list(server.id, RESOURCE_PACK_DIR),
    retry: false,
  });

  const worldPacksQuery = useQuery({
    queryKey: ["world-resource-packs", server.id],
    queryFn: async () => {
      const root = await api.files.list(server.id, "/");
      const dirs = root.entries.filter(
        (entry) =>
          entry.type === "dir" &&
          entry.name.toLowerCase() !== RESOURCE_PACK_DIR.slice(1),
      );
      const packs: WorldResourcePack[] = [];

      await Promise.all(
        dirs.map(async (dir) => {
          const worldPath = `/${dir.name}`;
          const listing = await api.files.list(server.id, worldPath).catch(() => null);
          const pack = listing?.entries.find(
            (entry) =>
              entry.type === "file" &&
              entry.name.toLowerCase() === "resources.zip",
          );
          if (pack) {
            packs.push({
              path: `${worldPath}/resources.zip`,
              worldName: dir.name,
              size: pack.size,
              modified: pack.modified,
            });
          }
        }),
      );

      return packs.sort((a, b) => a.worldName.localeCompare(b.worldName));
    },
    retry: false,
  });

  const zipFiles =
    filesQuery.data?.entries.filter(
      (entry) => entry.type === "file" && entry.name.toLowerCase().endsWith(".zip"),
    ) ?? [];
  const worldPacks = worldPacksQuery.data ?? [];

  useEffect(() => {
    const settings = getResourcePackSettings(server);
    const detected = worldPacks[0];
    if (
      settings.path ||
      !detected ||
      autoWorldDefaultRef.current === server.id
    ) {
      return;
    }

    autoWorldDefaultRef.current = server.id;
    setForm((prev) => {
      if (prev.path) return prev;
      return {
        ...prev,
        enabled: true,
        path: detected.path,
        sha1: "",
        file_name: "resources.zip",
        source: "world",
        required: true,
      };
    });
  }, [server, worldPacks]);

  const publicUrl = form.public_id
    ? api.resourcePacks.publicUrl(server.id, form.public_id)
    : "";

  const saveResourcePack = async (nextForm: typeof form) => {
    const publicId = nextForm.public_id || makePublicId();
    if (nextForm.enabled && !nextForm.path) {
      throw new Error("Choose or upload a resource pack zip first.");
    }
    const computedSHA1 = nextForm.enabled
      ? nextForm.sha1.trim() ||
        (await api.files
          .readBytes(server.id, nextForm.path)
          .then((bytes) => sha1Bytes(bytes)))
      : "";

    const advertisedUrl = nextForm.enabled
      ? api.resourcePacks.publicUrl(server.id, publicId)
      : "";
    const properties = await api.files
      .readContent(server.id, "/server.properties")
      .catch(() => defaultServerProperties);
    const values = {
      ...parseProperties(properties),
      "resource-pack": advertisedUrl,
      "resource-pack-id": "",
      "resource-pack-sha1": nextForm.enabled ? computedSHA1 : "",
      "require-resource-pack": String(nextForm.enabled && nextForm.required),
      "resource-pack-prompt": nextForm.enabled ? nextForm.prompt : "",
    };

    await api.files.writeContent(
      server.id,
      "/server.properties",
      serializeProperties(properties, values),
    );
    await api.servers.update(server.id, {
      settings: {
        ...server.settings,
        resource_pack: {
          enabled: nextForm.enabled,
          path: nextForm.path,
          public_id: publicId,
          sha1: computedSHA1,
          file_name: nextForm.file_name,
          source: nextForm.source,
          required: nextForm.required,
          prompt: nextForm.prompt,
          updated_at: new Date().toISOString(),
        },
      },
    });
    return { ...nextForm, public_id: publicId, sha1: computedSHA1 };
  };

  const saveMutation = useMutation({
    mutationFn: () => saveResourcePack(form),
    onSuccess: (saved) => {
      setForm(saved);
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      qc.invalidateQueries({
        queryKey: ["file-content", server.id, "/server.properties"],
      });
      success(
        "Resource pack settings saved",
        server.status === "online"
          ? "Restart the Minecraft server to make players receive the new pack."
          : undefined,
      );
    },
    onError: (e: Error) => error("Save failed", e.message),
  });

  const uploadMutation = useMutation({
    mutationFn: async (file: File) => {
      if (!file.name.toLowerCase().endsWith(".zip")) {
        throw new Error("Resource packs must be .zip files.");
      }
      const path = resourcePackPathFor(file.name);
      const storedName = path.split("/").pop() ?? "resource-pack.zip";
      const uploadFile = new File([file], storedName, {
        type: file.type || "application/zip",
        lastModified: file.lastModified,
      });
      const sha1 = await sha1Hex(file);
      await api.files.upload(server.id, RESOURCE_PACK_DIR, uploadFile, (p) =>
        setUploadPct(
          p.total > 0 ? Math.min(100, Math.round((p.loaded / p.total) * 100)) : 0,
        ),
      );
      const next = {
        ...form,
        enabled: true,
        path,
        public_id: form.public_id || makePublicId(),
        sha1,
        file_name: storedName,
        source: "uploaded" as const,
      };
      return saveResourcePack(next);
    },
    onSuccess: (saved) => {
      setForm(saved);
      qc.invalidateQueries({ queryKey: ["files", server.id, RESOURCE_PACK_DIR] });
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({
        queryKey: ["file-content", server.id, "/server.properties"],
      });
      success(
        "Resource pack uploaded",
        server.status === "online"
          ? "Restart the Minecraft server to make players receive the new pack."
          : undefined,
      );
    },
    onError: (e: Error) => error("Upload failed", e.message),
    onSettled: () => setUploadPct(null),
  });

  const copyUrl = async () => {
    await navigator.clipboard.writeText(publicUrl);
    success("Resource pack URL copied");
  };

  return (
    <Panel
      title="Resource Pack"
      description="Host one zip for this server and write its URL into server.properties."
      actions={
        <div className="flex flex-shrink-0 flex-wrap justify-end gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => fileInputRef.current?.click()}
            loading={uploadMutation.isPending}
          >
            {!uploadMutation.isPending && <Upload className="h-3.5 w-3.5" />}
            {uploadMutation.isPending && uploadPct !== null
              ? `${uploadPct}%`
              : "Upload"}
          </Button>
          <Button
            size="sm"
            onClick={() => saveMutation.mutate()}
            loading={saveMutation.isPending}
          >
            {!saveMutation.isPending && <Save className="h-3.5 w-3.5" />}
            Save
          </Button>
        </div>
      }
    >
      <input
        ref={fileInputRef}
        type="file"
        accept=".zip,application/zip"
        className="hidden"
        onChange={(e) => {
          const file = e.target.files?.[0];
          e.target.value = "";
          if (file) uploadMutation.mutate(file);
        }}
      />

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <div className="space-y-4">
          <label className="flex items-center gap-2 text-sm text-text-primary">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) =>
                setForm((p) => ({ ...p, enabled: e.target.checked }))
              }
            />
            Enabled
          </label>

          <div className="space-y-1.5">
            <Label>Hosted Zip</Label>
            <select
              className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
              value={form.path}
              onChange={(e) => {
                const path = e.target.value;
                const worldPack = worldPacks.find((pack) => pack.path === path);
                setForm((p) => ({
                  ...p,
                  path,
                  sha1: path === p.path ? p.sha1 : "",
                  file_name: path.split("/").pop() ?? p.file_name,
                  source: resourcePackSourceFor(path),
                  enabled: worldPack ? true : p.enabled,
                  required: worldPack ? true : p.required,
                }));
              }}
            >
              {form.path && <option value={form.path}>{form.path}</option>}
              <option value="">No hosted pack</option>
              {worldPacks.length > 0 && (
                <optgroup label="World resources.zip">
                  {worldPacks.map((pack) => (
                    <option key={pack.path} value={pack.path}>
                      {pack.worldName}/resources.zip
                    </option>
                  ))}
                </optgroup>
              )}
              {zipFiles.map((entry) => {
                const path = `${RESOURCE_PACK_DIR}/${entry.name}`;
                return (
                  <option key={path} value={path}>
                    {entry.name}
                  </option>
                );
              })}
            </select>
            {filesQuery.isError && (
              <p className="text-xs text-text-secondary">
                Upload a zip to create the resource-packs folder.
              </p>
            )}
          </div>

          <div className="space-y-1.5">
            <Label>SHA-1</Label>
            <Input
              value={form.sha1}
              onChange={(e) => setForm((p) => ({ ...p, sha1: e.target.value }))}
              className="font-mono"
              placeholder="Computed automatically on upload"
            />
          </div>

          <label className="flex items-center gap-2 text-sm text-text-primary">
            <input
              type="checkbox"
              checked={form.required}
              onChange={(e) =>
                setForm((p) => ({ ...p, required: e.target.checked }))
              }
            />
            Require pack
          </label>

          <div className="space-y-1.5">
            <Label>Prompt</Label>
            <Input
              value={form.prompt}
              onChange={(e) =>
                setForm((p) => ({ ...p, prompt: e.target.value }))
              }
              placeholder="Optional message shown to players"
            />
          </div>
        </div>

        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label>Public URL</Label>
            <div className="flex gap-2">
              <Input
                value={publicUrl}
                readOnly
                className="min-w-0 flex-1 font-mono"
              />
              <Button
                variant="outline"
                size="icon"
                onClick={copyUrl}
                title="Copy URL"
                disabled={!publicUrl}
              >
                <Copy className="h-4 w-4" />
              </Button>
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>Public ID</Label>
            <div className="flex gap-2">
              <Input
                value={form.public_id}
                onChange={(e) =>
                  setForm((p) => ({ ...p, public_id: e.target.value }))
                }
                className="min-w-0 flex-1 font-mono"
              />
              <Button
                variant="outline"
                size="icon"
                onClick={() =>
                  setForm((p) => ({ ...p, public_id: makePublicId() }))
                }
                title="Regenerate public ID"
              >
                <Link2 className="h-4 w-4" />
              </Button>
            </div>
          </div>
          <p className="text-xs leading-5 text-text-secondary">
            Saving writes resource-pack, resource-pack-sha1,
            require-resource-pack, and resource-pack-prompt in
            /server.properties.
          </p>
        </div>
      </div>
    </Panel>
  );
}

// ── Server icon ─────────────────────────────────────────────────────────────

// Minecraft loads a 64x64 server-icon.png from the server root at startup and
// shows it next to the MOTD in the multiplayer list. We let users drop in any
// image and normalise it to that exact format client-side, so they never have
