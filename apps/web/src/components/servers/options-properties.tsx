import { useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Copy, Link2, RotateCcw, Save, Trash2, Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { Server } from "@/lib/types";
import { Panel } from "./shared";

// ── Settings tab ──────────────────────────────────────────────────────────────

type PropertiesMap = Record<string, string>;

type PropertyFieldType =
  | "boolean"
  | "list"
  | "number"
  | "select"
  | "text"
  | "textarea";

type PropertyField = {
  key: string;
  label: string;
  type: PropertyFieldType;
  category: string;
  description?: string;
  options?: readonly string[];
  placeholder?: string;
  min?: number;
  max?: number;
  step?: number;
};

const propertyCategories = [
  "Basics",
  "Access",
  "World",
  "Gameplay",
  "Players",
  "Network",
  "Resource Pack",
  "Performance",
  "Admin",
  "Advanced",
  "Other",
] as const;

const propertyFields: PropertyField[] = [
  {
    key: "motd",
    label: "MOTD",
    type: "text",
    category: "Basics",
    placeholder: "A Minecraft Server",
  },
  {
    key: "level-name",
    label: "World Folder",
    type: "text",
    category: "Basics",
    placeholder: "world",
  },
  {
    key: "server-port",
    label: "Server Port",
    type: "number",
    category: "Basics",
    min: 1,
    max: 65535,
  },
  {
    key: "server-ip",
    label: "Bind IP",
    type: "text",
    category: "Basics",
    placeholder: "Leave blank for all interfaces",
  },
  {
    key: "online-mode",
    label: "Online Mode",
    type: "boolean",
    category: "Access",
  },
  {
    key: "white-list",
    label: "Whitelist",
    type: "boolean",
    category: "Access",
  },
  {
    key: "enforce-whitelist",
    label: "Enforce Whitelist",
    type: "boolean",
    category: "Access",
  },
  {
    key: "enforce-secure-profile",
    label: "Secure Profiles",
    type: "boolean",
    category: "Access",
  },
  {
    key: "prevent-proxy-connections",
    label: "Prevent Proxy Connections",
    type: "boolean",
    category: "Access",
  },
  {
    key: "accepts-transfers",
    label: "Accept Transfers",
    type: "boolean",
    category: "Access",
  },
  {
    key: "hide-online-players",
    label: "Hide Online Players",
    type: "boolean",
    category: "Access",
  },
  {
    key: "gamemode",
    label: "Game Mode",
    type: "select",
    category: "World",
    options: ["survival", "creative", "adventure", "spectator"],
  },
  {
    key: "difficulty",
    label: "Difficulty",
    type: "select",
    category: "World",
    options: ["peaceful", "easy", "normal", "hard"],
  },
  {
    key: "level-seed",
    label: "World Seed",
    type: "text",
    category: "World",
  },
  {
    key: "level-type",
    label: "World Type",
    type: "select",
    category: "World",
    options: [
      "minecraft:normal",
      "minecraft:flat",
      "minecraft:large_biomes",
      "minecraft:amplified",
      "minecraft:single_biome_surface",
    ],
  },
  {
    key: "generator-settings",
    label: "Generator Settings",
    type: "textarea",
    category: "World",
    placeholder: "{}",
  },
  {
    key: "generate-structures",
    label: "Generate Structures",
    type: "boolean",
    category: "World",
  },
  {
    key: "allow-nether",
    label: "Allow Nether",
    type: "boolean",
    category: "World",
  },
  {
    key: "max-world-size",
    label: "Max World Size",
    type: "number",
    category: "World",
    min: 1,
    max: 29999984,
  },
  {
    key: "hardcore",
    label: "Hardcore",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "pvp",
    label: "PVP",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "spawn-animals",
    label: "Spawn Animals",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "spawn-monsters",
    label: "Spawn Monsters",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "spawn-npcs",
    label: "Spawn NPCs",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "allow-flight",
    label: "Allow Flight",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "force-gamemode",
    label: "Force Game Mode",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "enable-command-block",
    label: "Command Blocks",
    type: "boolean",
    category: "Gameplay",
  },
  {
    key: "spawn-protection",
    label: "Spawn Protection",
    type: "number",
    category: "Gameplay",
    min: 0,
  },
  {
    key: "max-players",
    label: "Max Players",
    type: "number",
    category: "Players",
    min: 1,
  },
  {
    key: "player-idle-timeout",
    label: "Idle Timeout",
    type: "number",
    category: "Players",
    min: 0,
  },
  {
    key: "op-permission-level",
    label: "OP Permission Level",
    type: "number",
    category: "Players",
    min: 1,
    max: 4,
  },
  {
    key: "function-permission-level",
    label: "Function Permission Level",
    type: "number",
    category: "Players",
    min: 1,
    max: 4,
  },
  {
    key: "enable-status",
    label: "Show Server Status",
    type: "boolean",
    category: "Network",
  },
  {
    key: "enable-query",
    label: "Query",
    type: "boolean",
    category: "Network",
  },
  {
    key: "query.port",
    label: "Query Port",
    type: "number",
    category: "Network",
    min: 1,
    max: 65535,
  },
  {
    key: "enable-rcon",
    label: "RCON",
    type: "boolean",
    category: "Network",
  },
  {
    key: "rcon.port",
    label: "RCON Port",
    type: "number",
    category: "Network",
    min: 1,
    max: 65535,
  },
  {
    key: "rcon.password",
    label: "RCON Password",
    type: "text",
    category: "Network",
  },
  {
    key: "network-compression-threshold",
    label: "Compression Threshold",
    type: "number",
    category: "Network",
    min: -1,
  },
  {
    key: "rate-limit",
    label: "Rate Limit",
    type: "number",
    category: "Network",
    min: 0,
  },
  {
    key: "use-native-transport",
    label: "Native Transport",
    type: "boolean",
    category: "Network",
  },
  {
    key: "resource-pack",
    label: "Resource Pack URL",
    type: "text",
    category: "Resource Pack",
  },
  {
    key: "resource-pack-id",
    label: "Resource Pack ID",
    type: "text",
    category: "Resource Pack",
    placeholder: "UUID",
  },
  {
    key: "resource-pack-sha1",
    label: "Resource Pack SHA-1",
    type: "text",
    category: "Resource Pack",
  },
  {
    key: "require-resource-pack",
    label: "Require Resource Pack",
    type: "boolean",
    category: "Resource Pack",
  },
  {
    key: "resource-pack-prompt",
    label: "Resource Pack Prompt",
    type: "textarea",
    category: "Resource Pack",
  },
  {
    key: "view-distance",
    label: "View Distance",
    type: "number",
    category: "Performance",
    min: 2,
    max: 32,
  },
  {
    key: "simulation-distance",
    label: "Simulation Distance",
    type: "number",
    category: "Performance",
    min: 2,
    max: 32,
  },
  {
    key: "entity-broadcast-range-percentage",
    label: "Entity Broadcast Range",
    type: "number",
    category: "Performance",
    min: 10,
    max: 1000,
  },
  {
    key: "max-tick-time",
    label: "Max Tick Time",
    type: "number",
    category: "Performance",
    min: -1,
  },
  {
    key: "sync-chunk-writes",
    label: "Sync Chunk Writes",
    type: "boolean",
    category: "Performance",
  },
  {
    key: "pause-when-empty-seconds",
    label: "Pause When Empty",
    type: "number",
    category: "Performance",
    min: 0,
  },
  {
    key: "max-chained-neighbor-updates",
    label: "Max Neighbor Updates",
    type: "number",
    category: "Performance",
    min: -1,
  },
  {
    key: "region-file-compression",
    label: "Region Compression",
    type: "select",
    category: "Performance",
    options: ["deflate", "lz4", "none"],
  },
  {
    key: "broadcast-console-to-ops",
    label: "Broadcast Console to OPs",
    type: "boolean",
    category: "Admin",
  },
  {
    key: "broadcast-rcon-to-ops",
    label: "Broadcast RCON to OPs",
    type: "boolean",
    category: "Admin",
  },
  {
    key: "enable-jmx-monitoring",
    label: "JMX Monitoring",
    type: "boolean",
    category: "Admin",
  },
  {
    key: "log-ips",
    label: "Log IP Addresses",
    type: "boolean",
    category: "Admin",
  },
  {
    key: "bug-report-link",
    label: "Bug Report Link",
    type: "text",
    category: "Admin",
  },
  {
    key: "text-filtering-config",
    label: "Text Filtering Config",
    type: "text",
    category: "Advanced",
  },
  {
    key: "text-filtering-version",
    label: "Text Filtering Version",
    type: "number",
    category: "Advanced",
    min: 0,
  },
  {
    key: "initial-enabled-packs",
    label: "Initial Enabled Packs",
    type: "list",
    category: "Advanced",
    placeholder: "vanilla",
  },
  {
    key: "initial-disabled-packs",
    label: "Initial Disabled Packs",
    type: "list",
    category: "Advanced",
  },
];

const defaultServerProperties = `# Minecraft server properties
accepts-transfers=false
allow-flight=false
allow-nether=true
broadcast-console-to-ops=true
broadcast-rcon-to-ops=true
bug-report-link=
difficulty=easy
enable-command-block=false
enable-jmx-monitoring=false
enable-query=false
enable-rcon=false
enable-status=true
enforce-secure-profile=true
enforce-whitelist=false
entity-broadcast-range-percentage=100
force-gamemode=false
function-permission-level=2
gamemode=survival
generate-structures=true
generator-settings={}
hardcore=false
hide-online-players=false
initial-disabled-packs=
initial-enabled-packs=vanilla
level-name=world
level-seed=
level-type=minecraft:normal
log-ips=true
max-chained-neighbor-updates=1000000
max-players=20
max-tick-time=60000
max-world-size=29999984
motd=A Minecraft Server
network-compression-threshold=256
online-mode=true
op-permission-level=4
pause-when-empty-seconds=60
player-idle-timeout=0
prevent-proxy-connections=false
pvp=true
query.port=25565
rate-limit=0
rcon.password=
rcon.port=25575
region-file-compression=deflate
require-resource-pack=false
resource-pack=
resource-pack-id=
resource-pack-prompt=
resource-pack-sha1=
server-ip=
server-port=25565
simulation-distance=10
spawn-animals=true
spawn-monsters=true
spawn-npcs=true
spawn-protection=16
sync-chunk-writes=true
text-filtering-config=
text-filtering-version=0
use-native-transport=true
view-distance=10
white-list=false
`;

function parseProperties(content: string): PropertiesMap {
  const out: PropertiesMap = {};
  for (const line of content.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith("!"))
      continue;
    const idx = line.indexOf("=");
    if (idx === -1) continue;
    out[line.slice(0, idx).trim()] = line.slice(idx + 1);
  }
  return out;
}

function serializeProperties(original: string, values: PropertiesMap): string {
  const seen = new Set<string>();
  const lines = original.split(/\r?\n/).map((line) => {
    const trimmed = line.trim();
    const idx = line.indexOf("=");
    if (!trimmed || trimmed.startsWith("#") || idx === -1) return line;
    const key = line.slice(0, idx).trim();
    if (!(key in values)) return line;
    seen.add(key);
    return `${key}=${values[key]}`;
  });

  const missing = Object.entries(values)
    .filter(([key]) => !seen.has(key))
    .map(([key, value]) => `${key}=${value}`);

  return [...lines, ...missing].join("\n");
}

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

function getPropertyGroups(values: PropertiesMap) {
  const knownKeys = new Set(propertyFields.map((field) => field.key));
  const customFields: PropertyField[] = Object.keys(values)
    .filter((key) => !knownKeys.has(key))
    .sort((a, b) => a.localeCompare(b))
    .map((key) => ({
      key,
      label: key,
      type: "text",
      category: "Other",
    }));
  const allFields = [...propertyFields, ...customFields];

  return propertyCategories
    .map((category) => ({
      category,
      fields: allFields.filter((field) => field.category === category),
    }))
    .filter((group) => group.fields.length > 0);
}

function PropertyFieldControl({
  field,
  value,
  onChange,
}: {
  field: PropertyField;
  value: string;
  onChange: (value: string) => void;
}) {
  const inputClass =
    "flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent";

  if (field.type === "select") {
    return (
      <select
        className={inputClass}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">Default</option>
        {(field.options ?? []).map((option) => (
          <option key={option} value={option}>
            {option}
          </option>
        ))}
      </select>
    );
  }

  if (field.type === "boolean") {
    const enabled = value === "true";
    return (
      <button
        type="button"
        role="switch"
        aria-checked={enabled}
        onClick={() => onChange(enabled ? "false" : "true")}
        title={enabled ? "Disable" : "Enable"}
        className={`relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors ${
          enabled ? "bg-accent" : "border border-border-hover bg-background"
        }`}
      >
        <span
          className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${
            enabled ? "translate-x-[18px]" : "translate-x-0.5"
          }`}
        />
      </button>
    );
  }

  if (field.type === "textarea") {
    return (
      <textarea
        className="min-h-20 w-full resize-y rounded-md border border-border bg-surface-2 px-3 py-2 text-sm text-text-primary placeholder:text-text-secondary focus:outline-none focus:ring-2 focus:ring-accent"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={field.placeholder}
      />
    );
  }

  return (
    <Input
      type={field.type === "number" ? "number" : "text"}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      min={field.min}
      max={field.max}
      step={field.step}
      placeholder={field.placeholder}
      className={field.type === "list" ? "font-mono" : undefined}
    />
  );
}

function ServerPropertiesPanel({ serverId }: { serverId: string }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [values, setValues] = useState<PropertiesMap>({});
  const [saveBarPinned, setSaveBarPinned] = useState(false);
  const saveBarSentinelRef = useRef<HTMLDivElement>(null);

  const propsQuery = useQuery({
    queryKey: ["file-content", serverId, "/server.properties"],
    queryFn: () => api.files.readContent(serverId, "/server.properties"),
    retry: false,
  });

  const original = propsQuery.data ?? "";

  // Populate values during render (not in an effect) so the fields' first paint
  // already reflects the loaded data. Doing this in an effect would paint every
  // switch in its default "off" state for one frame, then flip the on-by-default
  // ones, making them animate on load.
  const [loadedFrom, setLoadedFrom] = useState<string | null>(null);
  if (propsQuery.data !== undefined && propsQuery.data !== loadedFrom) {
    setLoadedFrom(propsQuery.data);
    setValues(parseProperties(propsQuery.data));
  }

  useEffect(() => {
    const sentinel = saveBarSentinelRef.current;
    const scrollRoot = sentinel?.closest("[data-server-scroll]");
    if (!sentinel || !(scrollRoot instanceof HTMLElement)) return;

    const observer = new IntersectionObserver(
      ([entry]) => {
        const isPinned = entry.rootBounds
          ? entry.boundingClientRect.top < entry.rootBounds.top
          : !entry.isIntersecting;
        setSaveBarPinned(isPinned);
      },
      {
        root: scrollRoot,
        threshold: 1,
      },
    );

    observer.observe(sentinel);
    return () => observer.disconnect();
  }, []);

  const saveMutation = useMutation({
    mutationFn: () =>
      api.files.writeContent(
        serverId,
        "/server.properties",
        serializeProperties(original, values),
      ),
    onSuccess: () => {
      qc.invalidateQueries({
        queryKey: ["file-content", serverId, "/server.properties"],
      });
      success("server.properties saved");
    },
    onError: (e: Error) => error("Save failed", e.message),
  });

  const createMutation = useMutation({
    mutationFn: () =>
      api.files.writeContent(
        serverId,
        "/server.properties",
        defaultServerProperties,
      ),
    onSuccess: () => {
      qc.invalidateQueries({
        queryKey: ["file-content", serverId, "/server.properties"],
      });
      success("server.properties created");
    },
    onError: (e: Error) => error("Create failed", e.message),
  });

  const setProp = (key: string, value: string) => {
    setValues((prev) => ({ ...prev, [key]: value }));
  };

  return (
    <div>
      {/* Docked toolbar. Once the header below scrolls completely out of view,
          this full-width bar slides DOWN out from behind the page banner with
          a compact copy of the title + Save button. A zero-height sticky host
          keeps it pinned across the whole list without reserving layout; the
          bar is parked above the fold and clipped by the scroll container, so
          its top edge is flush with the banner for the entire slide. */}
      <div className="sticky top-0 z-30 h-0">
        <div
          className={`save-toolbar absolute -left-4 -right-4 top-0 flex items-center justify-between gap-4 border-b border-border bg-surface px-4 py-2.5 sm:-left-6 sm:-right-6 sm:px-6 ${
            saveBarPinned ? "save-toolbar--pinned" : "pointer-events-none"
          }`}
        >
          {/* Title lives in the normal-flow header below; the docked bar only
              re-surfaces the Save action once that header scrolls away, so it
              deliberately omits the title to avoid a duplicate "Minecraft
              Properties" heading. */}
          <span className="truncate text-xs text-text-secondary">
            Unsaved changes save to /server.properties
          </span>
          <Button
            size="sm"
            className="flex-shrink-0"
            onClick={() => saveMutation.mutate()}
            loading={saveMutation.isPending}
            disabled={propsQuery.isLoading || propsQuery.isError}
          >
            <Save className="h-3.5 w-3.5" /> Save Properties
          </Button>
        </div>
      </div>

      {/* Header — normal flow, scrolls away with the page. */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <h3 className="text-sm font-semibold text-text-primary">
            Minecraft Properties
          </h3>
          <p className="text-xs text-text-secondary mt-1">
            Edits /server.properties on the server.
          </p>
        </div>
        <Button
          size="sm"
          onClick={() => saveMutation.mutate()}
          loading={saveMutation.isPending}
          disabled={propsQuery.isLoading || propsQuery.isError}
        >
          <Save className="h-3.5 w-3.5" /> Save Properties
        </Button>
      </div>

      {/* Sentinel at the header's bottom edge: once it leaves the top of the
          scroll area, the header + its Save button are completely out of view
          — exactly the moment the docked toolbar drops down. */}
      <div ref={saveBarSentinelRef} className="h-px" aria-hidden="true" />

      <div className="mt-4">
        {propsQuery.isLoading ? (
        <div className="flex justify-center py-8">
          <div className="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin" />
        </div>
      ) : propsQuery.isError ? (
        <div className="rounded-md border border-border bg-surface p-4 text-sm text-text-secondary">
          <div className="flex items-center justify-between gap-4">
            <span>
              server.properties was not found yet. Start the server once, or
              create a default file.
            </span>
            <Button
              size="sm"
              onClick={() => createMutation.mutate()}
              loading={createMutation.isPending}
            >
              Create
            </Button>
          </div>
        </div>
      ) : (
        <div className="space-y-6">
          {getPropertyGroups(values).map((group) => (
            <section key={group.category} className="space-y-3">
              <h4 className="text-xs font-semibold uppercase tracking-wide text-text-secondary">
                {group.category}
              </h4>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                {group.fields.map((field) => (
                  field.type === "boolean" ? (
                    <div
                      key={field.key}
                      className="flex h-9 items-center justify-between gap-3 rounded-md border border-border bg-surface-2 px-3"
                    >
                      <Label title={field.key} className="min-w-0 truncate">
                        {field.label}
                      </Label>
                      <PropertyFieldControl
                        field={field}
                        value={values[field.key] ?? ""}
                        onChange={(value) => setProp(field.key, value)}
                      />
                    </div>
                  ) : (
                    <div
                      key={field.key}
                      className={
                        field.type === "textarea"
                          ? "space-y-1.5 sm:col-span-2"
                          : "space-y-1.5"
                      }
                    >
                      <Label title={field.key} className="block">
                        {field.label}
                      </Label>
                      <PropertyFieldControl
                        field={field}
                        value={values[field.key] ?? ""}
                        onChange={(value) => setProp(field.key, value)}
                      />
                    </div>
                  )
                ))}
              </div>
            </section>
          ))}
        </div>
        )}
      </div>
    </div>
  );
}

// VersionSelect shows a dropdown of known versions but keeps a "Custom…" escape
// hatch (and always preserves the current value even if it's not in the list).
function VersionSelect({
  value,
  onChange,
  options,
  loading,
  placeholder,
}: {
  value: string;
  onChange: (v: string) => void;
  options: string[];
  loading?: boolean;
  placeholder?: string;
}) {
  const known = options.includes(value);
  const [custom, setCustom] = useState(!known && value !== "");

  if (custom) {
    return (
      <div className="flex gap-1.5">
        <Input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          className="font-mono"
        />
        <Button
          size="sm"
          variant="ghost"
          onClick={() => {
            setCustom(false);
            onChange(options[0] ?? "");
          }}
          title="Pick from list"
        >
          List
        </Button>
      </div>
    );
  }

  return (
    <select
      className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
      value={value}
      onChange={(e) => {
        if (e.target.value === "__custom__") {
          setCustom(true);
          return;
        }
        onChange(e.target.value);
      }}
    >
      {loading && <option>Loading…</option>}
      {!known && value !== "" && <option value={value}>{value}</option>}
      {options.map((o) => (
        <option key={o} value={o}>
          {o}
        </option>
      ))}
      <option value="__custom__">Custom…</option>
    </select>
  );
}

export function SoftwareOptionsPanel({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [snapshots, setSnapshots] = useState(false);
  const [form, setForm] = useState({
    platform: server.platform,
    mc_version: server.mc_version,
    loader_version: server.loader_version ?? "",
    java_binary: server.java_binary,
    jvm_args: server.jvm_args.join(" "),
  });

  useEffect(() => {
    setForm({
      platform: server.platform,
      mc_version: server.mc_version,
      loader_version: server.loader_version ?? "",
      java_binary: server.java_binary,
      jvm_args: server.jvm_args.join(" "),
    });
  }, [server]);

  const versionsQuery = useQuery({
    queryKey: ["mc-versions", form.platform, snapshots],
    queryFn: () => api.minecraft.versions(form.platform, snapshots),
    staleTime: 30 * 60_000,
  });
  const loadersQuery = useQuery({
    queryKey: ["mc-loaders", form.platform],
    queryFn: () => api.minecraft.loaders(form.platform),
    staleTime: 30 * 60_000,
  });
  const mcOptions = (versionsQuery.data ?? []).map((v) => v.version);
  const loaderOptions = (loadersQuery.data ?? []).map((v) => v.version);

  const save = () =>
    api.servers.update(server.id, {
      platform: form.platform,
      mc_version: form.mc_version,
      loader_version: form.loader_version || null,
      java_binary: form.java_binary,
      jvm_args: form.jvm_args.trim() ? form.jvm_args.trim().split(/\s+/) : [],
    });

  const updateMutation = useMutation({
    mutationFn: save,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      success("Runtime settings saved");
    },
    onError: (e: Error) => error("Save failed", e.message),
  });

  // Save the new version then force the agent to re-fetch the runtime jar, so the
  // change actually applies (a normal save only takes effect if no jar exists).
  const reinstallMutation = useMutation({
    mutationFn: async () => {
      await save();
      await api.servers.reinstall(server.id);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      success(
        "Version applied",
        "Server stopped and runtime reinstalled — start it to run the new version.",
      );
    },
    onError: (e: Error) => error("Reinstall failed", e.message),
  });

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }));

  // Switching the Minecraft/loader version needs a runtime reinstall to take
  // effect; other edits (java binary, JVM args) only need a save.
  const versionChanged =
    form.platform !== server.platform ||
    form.mc_version !== server.mc_version ||
    (form.loader_version || "") !== (server.loader_version ?? "");
  const dirty =
    versionChanged ||
    form.java_binary !== server.java_binary ||
    form.jvm_args !== server.jvm_args.join(" ");

  return (
    <Panel
        title="Runtime"
        description="Choose the server runtime and version. Changing the Minecraft or loader version reinstalls the server runtime."
        actions={
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => updateMutation.mutate()}
              loading={updateMutation.isPending}
              disabled={!dirty}
            >
              {!updateMutation.isPending && <Save className="h-3.5 w-3.5" />}
              {dirty ? "Save" : "Saved"}
            </Button>
            {versionChanged && (
              <Button
                size="sm"
                onClick={() => reinstallMutation.mutate()}
                loading={reinstallMutation.isPending}
                className="whitespace-nowrap"
              >
                {!reinstallMutation.isPending && (
                  <RotateCcw className="h-3.5 w-3.5" />
                )}
                Apply &amp; reinstall
              </Button>
            )}
          </div>
        }
      >
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label>Type</Label>
            <select
              className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
              value={form.platform}
              onChange={f("platform")}
            >
              {[
                "vanilla",
                "paper",
                "purpur",
                "fabric",
                "forge",
                "neoforge",
                "quilt",
                "spigot",
              ].map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1.5">
            <div className="flex items-center justify-between">
              <Label>Minecraft Version</Label>
              <label className="flex items-center gap-1.5 text-xs text-text-secondary cursor-pointer">
                <input
                  type="checkbox"
                  checked={snapshots}
                  onChange={(e) => setSnapshots(e.target.checked)}
                />
                Snapshots
              </label>
            </div>
            <VersionSelect
              value={form.mc_version}
              onChange={(v) => setForm((p) => ({ ...p, mc_version: v }))}
              options={mcOptions}
              loading={versionsQuery.isFetching}
              placeholder="1.21.4"
            />
          </div>
          <div className="space-y-1.5">
            <Label>Loader Version</Label>
            {loaderOptions.length > 0 ? (
              <VersionSelect
                value={form.loader_version}
                onChange={(v) => setForm((p) => ({ ...p, loader_version: v }))}
                options={loaderOptions}
                loading={loadersQuery.isFetching}
                placeholder="Latest compatible"
              />
            ) : (
              <Input
                value={form.loader_version}
                onChange={f("loader_version")}
                placeholder="Latest compatible"
              />
            )}
          </div>
          <div className="space-y-1.5">
            <Label>Java Binary</Label>
            <Input
              value={form.java_binary}
              onChange={f("java_binary")}
              className="font-mono"
            />
          </div>
          <div className="col-span-2 space-y-1.5">
            <Label>JVM Arguments</Label>
            <Input
              value={form.jvm_args}
              onChange={f("jvm_args")}
              className="font-mono"
              placeholder="-XX:+UseG1GC"
            />
          </div>
        </div>
    </Panel>
  );
}

function ResourcePackOptionsPanel({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const autoWorldDefaultRef = useRef<string | null>(null);
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
      await api.files.upload(server.id, RESOURCE_PACK_DIR, uploadFile);
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
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => fileInputRef.current?.click()}
            loading={uploadMutation.isPending}
          >
            {!uploadMutation.isPending && <Upload className="h-3.5 w-3.5" />}
            Upload
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

function PanelOptionsPanel({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const navigate = useNavigate();
  const [showDelete, setShowDelete] = useState(false);
  const [purgeFiles, setPurgeFiles] = useState(false);
  const [purgeBackups, setPurgeBackups] = useState(false);

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
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
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

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }));

  return (
    <>
      <Panel
        title="Panel Options"
        description="Settings stored in the control panel database."
      >
        <div className="space-y-4 max-w-lg">
          <h3 className="text-sm font-semibold text-text-primary">General</h3>
          <div className="space-y-1.5">
            <Label>Name</Label>
            <Input value={form.name} onChange={f("name")} />
          </div>
          <div className="space-y-1.5">
            <Label>Description</Label>
            <Input
              value={form.description}
              onChange={f("description")}
              placeholder="Optional…"
            />
          </div>
          <div className="space-y-1.5">
            <Label>Server Directory</Label>
            <Input
              value={form.directory_path}
              onChange={f("directory_path")}
              className="font-mono"
              placeholder="E:/mc-test"
            />
          </div>
        </div>

        <div className="space-y-4">
          <h3 className="text-sm font-semibold text-text-primary">Resources</h3>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label>Port</Label>
              <Input type="number" value={form.port} onChange={f("port")} />
            </div>
            <div className="space-y-1.5">
              <Label>Max RAM (MB)</Label>
              <Input
                type="number"
                value={form.ram_mb_max}
                onChange={f("ram_mb_max")}
              />
            </div>
            <div className="space-y-1.5">
              <Label>Min RAM (MB)</Label>
              <Input
                type="number"
                value={form.ram_mb_min}
                onChange={f("ram_mb_min")}
              />
            </div>
          </div>
        </div>

        <div className="flex justify-end gap-3">
          <Button
            onClick={() => updateMutation.mutate()}
            loading={updateMutation.isPending}
          >
            Save Changes
          </Button>
        </div>
      </Panel>

      <div className="border-t border-border pt-6 space-y-3">
        <h3 className="text-sm font-semibold text-red-400">Danger Zone</h3>
        <p className="text-sm text-text-secondary">
          Deleting a server removes it from the panel. By default the files on
          the node are kept — tick the options below to also wipe them from disk.
        </p>
        <Button variant="destructive" size="sm" onClick={openDelete}>
          <Trash2 className="h-3.5 w-3.5" /> Delete Server
        </Button>
      </div>

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

export function OptionsTab({ server }: { server: Server }) {
  return (
    <div className="max-w-3xl space-y-5">
      <ResourcePackOptionsPanel server={server} />
      <PanelOptionsPanel server={server} />
    </div>
  );
}

export function PropertiesTab({ serverId }: { serverId: string }) {
  return (
    <div className="max-w-5xl pt-4 sm:pt-6">
      <ServerPropertiesPanel serverId={serverId} />
    </div>
  );
}
