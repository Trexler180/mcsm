import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Compass, Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/dialog";
import { ModDetailDialog } from "@/components/mods/detail";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { InstalledMod, ModSource } from "@/lib/types";
import { compatible, type DetailTarget } from "./shared";
import { VersionSwitchDialog } from "./version-switch-dialog";
import { InstalledTab } from "./installed-tab";
import { BrowseTab } from "./browse-tab";

interface ModSearchProps {
  serverId: string;
  /** Server platform, doubles as the Modrinth loader facet for mods. */
  loader: string;
  mcVersion: string;
  /** Server platform, used to fetch the Minecraft version list. */
  platform: string;
}

// ModSearch is the container for the Mods pane: it owns the tab bar, the
// queries both tabs need, the upload input, and the dialogs. The tab bodies
// live in InstalledTab / BrowseTab; both stay mounted so their filter state
// and in-flight polling survive tab switches, exactly as before the split.
export function ModSearch({
  serverId,
  loader,
  mcVersion,
  platform,
}: ModSearchProps) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [activeTab, setActiveTab] = useState<"installed" | "search">(
    "installed",
  );
  // Result total reported up from BrowseTab for the tab label (null until the
  // first search resolves).
  const [browseTotal, setBrowseTotal] = useState<number | null>(null);
  const uploadInputRef = useRef<HTMLInputElement>(null);

  const { data: sources } = useQuery({
    queryKey: ["mod-sources", serverId],
    queryFn: () => api.mods.sources(serverId),
    staleTime: 60 * 60_000,
  });
  const curseforgeEnabled = sources?.curseforge ?? false;

  const [uninstallTarget, setUninstallTarget] = useState<InstalledMod | null>(
    null,
  );
  const [switchTarget, setSwitchTarget] = useState<InstalledMod | null>(null);
  const [detailTarget, setDetailTarget] = useState<DetailTarget | null>(null);
  const [detailConfirm, setDetailConfirm] = useState(false);
  const [detailForcing, setDetailForcing] = useState(false);

  const {
    data: installed = [],
    isLoading: loadingInstalled,
    isFetching: refreshingInstalled,
  } = useQuery({
    queryKey: ["mods", serverId],
    queryFn: () => api.mods.list(serverId),
  });

  const {
    data: updates = [],
    isLoading: loadingUpdates,
    isFetching: refreshingUpdates,
  } = useQuery({
    queryKey: ["mod-updates", serverId],
    queryFn: () => api.mods.updates(serverId),
    staleTime: 5 * 60_000,
  });
  const refreshingContent = refreshingInstalled || refreshingUpdates;

  const refreshInstalledContent = async () => {
    await Promise.all([
      qc.invalidateQueries({ queryKey: ["mods", serverId] }),
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] }),
    ]);
  };

  const uninstallMutation = useMutation({
    mutationFn: (modId: string) => api.mods.uninstall(serverId, modId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      success("Mod uninstalled");
      setUninstallTarget(null);
    },
    onError: (e: Error) => error("Uninstall failed", e.message),
  });

  const [customUploadPct, setCustomUploadPct] = useState<number | null>(null);
  const uploadCustomMutation = useMutation({
    mutationFn: (files: File[]) => {
      if (files.length === 0) {
        throw new Error("Choose one or more jar files.");
      }
      const invalid = files.find(
        (file) => !file.name.toLowerCase().endsWith(".jar"),
      );
      if (invalid) {
        throw new Error(`${invalid.name} is not a jar file.`);
      }
      return api.mods.uploadCustom(serverId, files, (p) =>
        setCustomUploadPct(
          p.total > 0 ? Math.min(100, Math.round((p.loaded / p.total) * 100)) : 0,
        ),
      );
    },
    onSuccess: (mods) => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] });
      setActiveTab("installed");
      success(
        mods.length === 1 ? "Custom jar uploaded" : "Custom jars uploaded",
        mods.map((m) => m.file_name).join(", "),
      );
    },
    onError: (e: Error) => error("Upload failed", e.message),
    onSettled: () => setCustomUploadPct(null),
  });

  // Install from the detail dialog. versionId "" → latest compatible; an explicit
  // id force-installs that exact file (used for the mismatch override).
  const detailInstallMutation = useMutation({
    mutationFn: (versionId: string) => {
      if (!detailTarget) return Promise.resolve();
      return detailTarget.isModpack
        ? api.mods.installModpack(serverId, detailTarget.projectId, versionId)
        : api.mods.install(
            serverId,
            detailTarget.source,
            detailTarget.projectId,
            versionId,
            true,
          );
    },
    onSuccess: (created: unknown) => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      const list = Array.isArray(created) ? (created as InstalledMod[]) : [];
      const deps = list.slice(1).map((m) => m.name);
      success(
        "Mod installed",
        deps.length > 0
          ? `Pulled dependencies: ${deps.join(", ")}`
          : "No required dependencies",
      );
      setDetailTarget(null);
      setDetailConfirm(false);
    },
    onError: (e: Error) => error("Install failed", e.message),
  });

  const handleDetailInstall = () => {
    if (!detailTarget) return;
    const hit = detailTarget.hit;
    // Only the browse-originated detail dialog carries a hit to gate on.
    if (
      hit &&
      !compatible(
        hit,
        detailTarget.source === "modrinth" ? loader : "",
        detailTarget.source === "spigotmc" ? "" : mcVersion,
        detailTarget.projectType ?? "mod",
      )
    ) {
      setDetailConfirm(true);
      return;
    }
    detailInstallMutation.mutate("");
  };

  const handleDetailForce = async () => {
    if (!detailTarget) return;
    setDetailForcing(true);
    try {
      const versions = await api.mods.getVersions(
        serverId,
        detailTarget.projectId,
        "",
        "",
        detailTarget.source,
      );
      if (versions.length === 0) {
        throw new Error("no downloadable versions for this project");
      }
      detailInstallMutation.mutate(versions[0].id);
    } catch (e) {
      error("Install failed", (e as Error).message);
    } finally {
      setDetailForcing(false);
    }
  };

  const handleCustomUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files ?? []);
    e.target.value = "";
    if (files.length > 0) {
      uploadCustomMutation.mutate(files);
    }
  };

  return (
    <div className="flex flex-col h-full">
      <input
        ref={uploadInputRef}
        type="file"
        accept=".jar"
        multiple
        className="hidden"
        onChange={handleCustomUpload}
      />
      {/* Tab bar */}
      <div className="flex-shrink-0 flex border-b border-border bg-surface items-center">
        <button
          onClick={() => setActiveTab("installed")}
          className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
            activeTab === "installed"
              ? "text-text-primary border-accent"
              : "text-text-secondary border-transparent hover:text-text-primary"
          }`}
        >
          Installed ({installed.length})
        </button>
        <button
          onClick={() => setActiveTab("search")}
          className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
            activeTab === "search"
              ? "text-text-primary border-accent"
              : "text-text-secondary border-transparent hover:text-text-primary"
          }`}
        >
          Browse
          {browseTotal !== null && ` (${browseTotal})`}
        </button>
        <div className="ml-auto flex items-center gap-2 mr-2">
          <Button
            size="sm"
            variant={activeTab === "installed" ? "outline" : "ghost"}
            onClick={() => uploadInputRef.current?.click()}
            loading={uploadCustomMutation.isPending}
            title="Upload custom jar"
          >
            {!uploadCustomMutation.isPending && <Upload className="h-3.5 w-3.5" />}
            {uploadCustomMutation.isPending && customUploadPct !== null
              ? `${customUploadPct}%`
              : "Upload jar"}
          </Button>
          {activeTab === "installed" && (
            <Button
              size="sm"
              variant="default"
              onClick={() => setActiveTab("search")}
            >
              <Compass className="h-3.5 w-3.5" />
              Browse content
            </Button>
          )}
        </div>
      </div>

      <InstalledTab
        serverId={serverId}
        active={activeTab === "installed"}
        installed={installed}
        updates={updates}
        loadingInstalled={loadingInstalled}
        loadingUpdates={loadingUpdates}
        refreshingContent={refreshingContent}
        onRefresh={refreshInstalledContent}
        onUninstall={setUninstallTarget}
        onSwitchVersion={setSwitchTarget}
        onShowDetails={(mod) =>
          setDetailTarget({
            source: mod.source as ModSource,
            projectId: mod.source_id!,
            isModpack: false,
            installed: true,
          })
        }
        onUploadClick={() => uploadInputRef.current?.click()}
        uploadPending={uploadCustomMutation.isPending}
        uploadPct={customUploadPct}
      />

      <BrowseTab
        serverId={serverId}
        active={activeTab === "search"}
        loader={loader}
        mcVersion={mcVersion}
        platform={platform}
        curseforgeEnabled={curseforgeEnabled}
        installed={installed}
        onShowDetails={setDetailTarget}
        onTotalHits={setBrowseTotal}
      />

      <ConfirmDialog
        open={uninstallTarget !== null}
        onClose={() => setUninstallTarget(null)}
        onConfirm={() =>
          uninstallTarget && uninstallMutation.mutate(uninstallTarget.id)
        }
        title="Uninstall mod"
        description={`Uninstall "${uninstallTarget?.name}"? The file will be removed from the server.`}
        confirmLabel="Uninstall"
        variant="destructive"
        loading={uninstallMutation.isPending}
      />

      <VersionSwitchDialog
        open={switchTarget !== null}
        onClose={() => setSwitchTarget(null)}
        serverId={serverId}
        mod={switchTarget}
        loader={loader}
        // SpigotMC version entries inherit the resource's stale major-only
        // "tested versions", so the MC compat tag would mislead — skip it.
        mcVersion={switchTarget?.source === "spigotmc" ? "" : mcVersion}
      />

      <ConfirmDialog
        open={detailConfirm}
        onClose={() => setDetailConfirm(false)}
        onConfirm={handleDetailForce}
        title="Install anyway?"
        description={`This project doesn't list compatibility with your server (${loader || "any loader"} · ${mcVersion || "any version"}). Installing the newest available file may not load correctly.`}
        confirmLabel="Install anyway"
        variant="destructive"
        loading={detailForcing || detailInstallMutation.isPending}
      />

      {detailTarget && (
        <ModDetailDialog
          open={detailTarget !== null}
          onClose={() => setDetailTarget(null)}
          serverId={serverId}
          source={detailTarget.source}
          projectId={detailTarget.projectId}
          slug={detailTarget.slug}
          installed={detailTarget.installed}
          installing={detailInstallMutation.isPending}
          onInstall={detailTarget.installed ? undefined : handleDetailInstall}
        />
      )}
    </div>
  );
}
