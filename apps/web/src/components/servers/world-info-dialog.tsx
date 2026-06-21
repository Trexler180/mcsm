import { useQuery } from "@tanstack/react-query";
import { Globe2, Loader2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Dialog } from "@/components/ui/dialog";
import { api } from "@/lib/api";
import {
  parseNbt,
  TAG_COMPOUND,
  type NbtEntry,
  type NbtTag,
} from "@/lib/nbt";

// ── NBT navigation helpers ─────────────────────────────────────────────────

function entriesOf(tag: NbtTag | undefined): NbtEntry[] | undefined {
  if (tag && tag.type === TAG_COMPOUND) return tag.value as NbtEntry[];
  return undefined;
}

function field(
  entries: NbtEntry[] | undefined,
  name: string,
): NbtTag | undefined {
  return entries?.find((e) => e.name === name)?.tag;
}

function asNumber(tag: NbtTag | undefined): number | undefined {
  if (!tag) return undefined;
  if (typeof tag.value === "number") return tag.value;
  if (typeof tag.value === "bigint") return Number(tag.value);
  return undefined;
}

function asString(tag: NbtTag | undefined): string | undefined {
  return tag && typeof tag.value === "string" ? tag.value : undefined;
}

function asBigInt(tag: NbtTag | undefined): bigint | undefined {
  if (!tag) return undefined;
  if (typeof tag.value === "bigint") return tag.value;
  if (typeof tag.value === "number") return BigInt(Math.trunc(tag.value));
  return undefined;
}

const GAME_MODES = ["Survival", "Creative", "Adventure", "Spectator"];
const DIFFICULTIES = ["Peaceful", "Easy", "Normal", "Hard"];

// Map a known dimension/sub-folder name to a friendly label.
const DIMENSION_LABELS: Record<string, string> = {
  region: "Overworld",
  "DIM-1": "The Nether",
  DIM1: "The End",
};

interface WorldInfo {
  levelName?: string;
  versionName?: string;
  snapshot?: boolean;
  dataVersion?: number;
  gameMode?: string;
  difficulty?: string;
  hardcore?: boolean;
  cheats?: boolean;
  seed?: string;
  spawn?: { x: number; y: number; z: number };
  dayCount?: number;
  clock?: string;
  lastPlayed?: string;
  weather?: string;
}

// Convert the in-game DayTime tick counter into a 24h clock. Tick 0 == 06:00.
function ticksToClock(dayTime: number): string {
  const t = ((dayTime % 24000) + 24000) % 24000;
  const totalMinutes = ((t / 1000 + 6) % 24) * 60;
  const h = Math.floor(totalMinutes / 60);
  const m = Math.floor(totalMinutes % 60);
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`;
}

function parseWorldInfo(tag: NbtTag): WorldInfo {
  // level.dat is a Compound whose single child "Data" holds the level state.
  const data = entriesOf(field(entriesOf(tag), "Data"));
  if (!data) return {};

  const version = entriesOf(field(data, "Version"));
  const worldGen = entriesOf(field(data, "WorldGenSettings"));

  const gameType = asNumber(field(data, "GameType"));
  const difficulty = asNumber(field(data, "Difficulty"));
  const time = asBigInt(field(data, "Time"));
  const dayTime = asNumber(field(data, "DayTime"));
  const lastPlayedMs = asBigInt(field(data, "LastPlayed"));
  const seed =
    asBigInt(field(worldGen, "seed")) ?? asBigInt(field(data, "RandomSeed"));

  const spawnX = asNumber(field(data, "SpawnX"));
  const spawnY = asNumber(field(data, "SpawnY"));
  const spawnZ = asNumber(field(data, "SpawnZ"));

  const raining = asNumber(field(data, "raining"));
  const thundering = asNumber(field(data, "thundering"));
  const weather = thundering
    ? "Thunderstorm"
    : raining
      ? "Raining"
      : raining === 0
        ? "Clear"
        : undefined;

  return {
    levelName: asString(field(data, "LevelName")),
    versionName: asString(field(version, "Name")),
    snapshot: asNumber(field(version, "Snapshot")) === 1,
    dataVersion: asNumber(field(data, "DataVersion")),
    gameMode:
      gameType !== undefined ? GAME_MODES[gameType] ?? `#${gameType}` : undefined,
    difficulty:
      difficulty !== undefined
        ? DIFFICULTIES[difficulty] ?? `#${difficulty}`
        : undefined,
    hardcore: asNumber(field(data, "hardcore")) === 1,
    cheats: asNumber(field(data, "allowCommands")) === 1,
    seed: seed !== undefined ? seed.toString() : undefined,
    spawn:
      spawnX !== undefined && spawnY !== undefined && spawnZ !== undefined
        ? { x: spawnX, y: spawnY, z: spawnZ }
        : undefined,
    dayCount: time !== undefined ? Math.floor(Number(time) / 24000) : undefined,
    clock: dayTime !== undefined ? ticksToClock(dayTime) : undefined,
    lastPlayed:
      lastPlayedMs !== undefined && lastPlayedMs > 0n
        ? new Date(Number(lastPlayedMs)).toLocaleString()
        : undefined,
    weather,
  };
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 py-2">
      <span className="text-xs text-text-secondary">{label}</span>
      <span className="truncate text-right text-sm font-medium text-text-primary">
        {value}
      </span>
    </div>
  );
}

export function WorldInfoDialog({
  serverId,
  worldName,
  onClose,
}: {
  serverId: string;
  worldName: string;
  onClose: () => void;
}) {
  // level.dat: read raw bytes, gunzip + parse NBT, then pull the fields we show.
  const {
    data: info,
    isLoading: infoLoading,
    error: infoError,
  } = useQuery({
    queryKey: ["world-info", serverId, worldName],
    queryFn: async () => {
      const bytes = await api.files.readBytes(
        serverId,
        `/${worldName}/level.dat`,
      );
      const { root } = await parseNbt(bytes);
      return parseWorldInfo(root.tag);
    },
  });

  // Top-level folder contents — used to surface which dimensions exist.
  const { data: listing } = useQuery({
    queryKey: ["files", serverId, `/${worldName}`],
    queryFn: () => api.files.list(serverId, `/${worldName}`),
  });

  const dimensions = (listing?.entries ?? [])
    .filter((e) => e.type === "dir" && DIMENSION_LABELS[e.name])
    .map((e) => DIMENSION_LABELS[e.name]);

  const rows: Array<{ label: string; value: string }> = [];
  if (info?.versionName) {
    rows.push({
      label: "Minecraft version",
      value: info.snapshot ? `${info.versionName} (snapshot)` : info.versionName,
    });
  }
  if (info?.gameMode) rows.push({ label: "Game mode", value: info.gameMode });
  if (info?.difficulty)
    rows.push({ label: "Difficulty", value: info.difficulty });
  if (info?.seed) rows.push({ label: "Seed", value: info.seed });
  if (info?.spawn)
    rows.push({
      label: "Spawn point",
      value: `${info.spawn.x}, ${info.spawn.y}, ${info.spawn.z}`,
    });
  if (info?.dayCount !== undefined)
    rows.push({
      label: "Time played",
      value: `Day ${info.dayCount.toLocaleString()}`,
    });
  if (info?.clock) rows.push({ label: "Time of day", value: info.clock });
  if (info?.weather) rows.push({ label: "Weather", value: info.weather });
  if (dimensions.length)
    rows.push({ label: "Dimensions", value: dimensions.join(", ") });
  if (info?.lastPlayed)
    rows.push({ label: "Last played", value: info.lastPlayed });
  if (info?.dataVersion !== undefined)
    rows.push({ label: "Data version", value: String(info.dataVersion) });

  return (
    <Dialog
      open
      onClose={onClose}
      title={info?.levelName || worldName}
      description={worldName}
      titleIcon={<Globe2 className="h-5 w-5 flex-shrink-0 text-accent" />}
    >
      {infoLoading ? (
        <div className="flex justify-center py-10">
          <Loader2 className="h-5 w-5 animate-spin text-accent" />
        </div>
      ) : infoError ? (
        <p className="py-6 text-center text-sm text-text-secondary">
          Couldn't read this world's <span className="font-mono">level.dat</span>
          . It may not have generated yet.
        </p>
      ) : (
        <>
          {(info?.hardcore || info?.cheats) && (
            <div className="mb-3 flex flex-wrap gap-2">
              {info?.hardcore && <Badge variant="error">Hardcore</Badge>}
              {info?.cheats && <Badge variant="muted">Cheats enabled</Badge>}
            </div>
          )}
          {rows.length === 0 ? (
            <p className="py-6 text-center text-sm text-text-secondary">
              No level details found.
            </p>
          ) : (
            <div className="divide-y divide-border">
              {rows.map((r) => (
                <InfoRow key={r.label} label={r.label} value={r.value} />
              ))}
            </div>
          )}
        </>
      )}
    </Dialog>
  );
}
