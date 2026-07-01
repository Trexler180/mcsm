import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Package,
  Download,
  Loader2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/dialog";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type {
  InstalledMod,
  ModProjectType,
  ModSearchHit,
  ModSource,
} from "@/lib/types";
import { compatible, environmentTag, formatDownloads } from "./shared";

export function SearchHitCard({
  hit,
  serverId,
  source,
  isModpack,
  projectType,
  loader,
  mcVersion,
  serverLoader,
  serverMc,
  installedIds,
  onShowDetails,
}: {
  hit: ModSearchHit;
  serverId: string;
  source: ModSource;
  isModpack: boolean;
  projectType: ModProjectType;
  /** Browse-filter loader/mc — drives the version picker dropdown. */
  loader: string;
  mcVersion: string;
  /** Server's real loader/mc — drives the install compatibility gate. */
  serverLoader: string;
  serverMc: string;
  installedIds: Set<string>;
  onShowDetails: () => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [picking, setPicking] = useState(false);
  const [selectedVersionId, setSelectedVersionId] = useState<string>("");
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [forcing, setForcing] = useState(false);

  const versionsQuery = useQuery({
    queryKey: ["mod-versions", source, serverId, hit.project_id, loader, mcVersion],
    queryFn: () =>
      api.mods.getVersions(serverId, hit.project_id, loader, mcVersion, source),
    enabled: picking,
  });

  const installMutation = useMutation({
    mutationFn: (versionId: string) =>
      isModpack
        ? api.mods.installModpack(serverId, hit.project_id, versionId)
        : api.mods.install(serverId, source, hit.project_id, versionId, true),
    onSuccess: (created: unknown) => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      setConfirmOpen(false);
      // installRecursive returns [mainMod, ...dependencies]; name the deps so
      // the user can see exactly what got pulled in alongside their pick.
      const list = Array.isArray(created) ? (created as InstalledMod[]) : [];
      const deps = list.slice(1).map((m) => m.name);
      success(
        `Installed ${hit.title}`,
        deps.length > 0
          ? `Pulled ${deps.length} dependenc${deps.length === 1 ? "y" : "ies"}: ${deps.join(", ")}`
          : "No required dependencies",
      );
    },
    onError: (e: Error) => error("Install failed", e.message),
  });

  const envTag = environmentTag(hit.client_side, hit.server_side);
  const isInstalled = installedIds.has(hit.project_id);
  // SpigotMC "tested versions" are major-only and often stale, so skip the MC
  // gate there rather than flagging nearly everything incompatible.
  const isCompatible = compatible(
    hit,
    source === "modrinth" ? serverLoader : "",
    source === "spigotmc" ? "" : serverMc,
    projectType,
  );

  const handleQuickInstall = () => {
    if (isInstalled) return;
    if (!isCompatible) {
      setConfirmOpen(true);
      return;
    }
    // Empty versionId → backend resolves latest compatible.
    installMutation.mutate("");
  };

  // Force-install ignores the server's loader/MC: fetch the newest file with no
  // version filters and install it by explicit id (skips compat resolution).
  const handleForceInstall = async () => {
    setForcing(true);
    try {
      const versions = await api.mods.getVersions(
        serverId,
        hit.project_id,
        "",
        "",
        source,
      );
      if (versions.length === 0) {
        throw new Error("no downloadable versions for this project");
      }
      installMutation.mutate(versions[0].id);
    } catch (e) {
      error("Install failed", (e as Error).message);
    } finally {
      setForcing(false);
    }
  };

  return (
    <div className="rounded-lg border border-border bg-surface hover:border-border/80 transition-colors p-3">
      <div className="flex gap-3">
        <button
          onClick={onShowDetails}
          className="flex-shrink-0"
          title="View details"
        >
          {hit.icon_url ? (
            <img
              src={hit.icon_url}
              alt=""
              className="h-14 w-14 rounded-md object-cover"
            />
          ) : (
            <div className="h-14 w-14 rounded-md bg-surface-2 flex items-center justify-center">
              <Package className="h-6 w-6 text-text-secondary" />
            </div>
          )}
        </button>

        <div className="flex-1 min-w-0">
          <button
            onClick={onShowDetails}
            className="text-left group block max-w-full"
            title="View details"
          >
            <span className="text-sm font-semibold text-text-primary group-hover:text-accent transition-colors">
              {hit.title}
            </span>
            {hit.author && (
              <span className="text-xs text-text-secondary"> by {hit.author}</span>
            )}
          </button>
          <p className="text-xs text-text-secondary line-clamp-2 mt-0.5">
            {hit.description}
          </p>
          {(envTag || hit.categories.length > 0) && (
            <div className="flex flex-wrap gap-1 mt-1.5">
              {envTag && (
                <span
                  className={`text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded border ${envTag.className}`}
                  title={envTag.title}
                >
                  {envTag.label}
                </span>
              )}
              {hit.categories.slice(0, 5).map((c) => (
                <span
                  key={c}
                  className="text-[10px] px-1.5 py-0.5 rounded bg-surface-2 text-text-secondary border border-border/50"
                >
                  {c}
                </span>
              ))}
            </div>
          )}
        </div>

        {/* Metrics + action */}
        <div className="flex flex-col items-end justify-between flex-shrink-0 gap-2">
          <div className="text-right text-xs text-text-secondary space-y-0.5">
            <div className="flex items-center justify-end gap-1">
              <Download className="h-3 w-3" />
              {formatDownloads(hit.downloads)}
            </div>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-1">
            {!isInstalled && !isCompatible && (
              <span
                className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-400 border border-amber-500/30"
                title="Does not match the server's loader / Minecraft version"
              >
                mismatch
              </span>
            )}
            {isInstalled ? (
              <span className="text-xs text-green-400 px-2 py-1 font-medium">
                Installed
              </span>
            ) : (
              <>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => setPicking((v) => !v)}
                  title="Choose version"
                >
                  {picking ? "Hide" : "Versions"}
                </Button>
                <Button
                  size="sm"
                  variant="default"
                  onClick={handleQuickInstall}
                  loading={installMutation.isPending && !confirmOpen}
                >
                  <Download className="h-3.5 w-3.5" />
                  Install
                </Button>
              </>
            )}
          </div>
        </div>
      </div>

      {picking && !isInstalled && (
        <div className="mt-3 pl-[4.25rem] flex items-center gap-2">
          {versionsQuery.isFetching ? (
            <Loader2 className="h-4 w-4 animate-spin text-accent" />
          ) : versionsQuery.data && versionsQuery.data.length > 0 ? (
            <>
              <select
                className="flex-1 text-xs rounded bg-surface-2 border border-border px-2 py-1 text-text-primary"
                value={selectedVersionId || versionsQuery.data[0].id}
                onChange={(e) => setSelectedVersionId(e.target.value)}
              >
                {versionsQuery.data.map((v) => (
                  <option key={v.id} value={v.id}>
                    {v.version_number} · {v.game_versions.join(", ")}
                  </option>
                ))}
              </select>
              <Button
                size="sm"
                onClick={() =>
                  installMutation.mutate(
                    selectedVersionId || versionsQuery.data![0].id,
                  )
                }
                loading={installMutation.isPending}
              >
                Install
              </Button>
            </>
          ) : (
            <p className="text-xs text-text-secondary">
              No compatible versions for {loader || "any loader"}{" "}
              {mcVersion || "any version"}
            </p>
          )}
        </div>
      )}

      <ConfirmDialog
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        onConfirm={handleForceInstall}
        title="Install anyway?"
        description={`"${hit.title}" doesn't list compatibility with your server (${serverLoader || "any loader"} · ${serverMc || "any version"}). Installing the newest available file may not load correctly.`}
        confirmLabel="Install anyway"
        variant="destructive"
        loading={forcing || installMutation.isPending}
      />
    </div>
  );
}
