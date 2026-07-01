import type { } from "@/lib/types";


// ── Settings tab ──────────────────────────────────────────────────────────────

export type PropertiesMap = Record<string, string>;

type PropertyFieldType =
  | "boolean"
  | "list"
  | "number"
  | "select"
  | "text"
  | "textarea";

export type PropertyField = {
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

export const propertyCategories = [
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

export const propertyFields: PropertyField[] = [
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

export const defaultServerProperties = `# Minecraft server properties
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

export function parseProperties(content: string): PropertiesMap {
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

export function serializeProperties(original: string, values: PropertiesMap): string {
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

export function getPropertyGroups(values: PropertiesMap) {
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
