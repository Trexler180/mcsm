import { useEffect, useMemo, useState, type ComponentProps } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  Search,
  Package,
  Download,
  Loader2,
  AlertTriangle,
  FileText,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Dialog } from "@/components/ui/dialog";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type {
  InstalledMod,
  ModSource,
  ModVersion,
} from "@/lib/types";
import { formatVersionDate, versionCompatible, versionTypeClass } from "./shared";

export function switchActionLabel(
  selected: ModVersion | null,
  current: ModVersion | null,
  versions: ModVersion[],
): string {
  if (!selected) return "Switch";
  if (current && selected.id === current.id) return "Current version";
  if (!current) return `Switch to ${selected.version_number}`;

  const selectedIndex = versions.findIndex((v) => v.id === selected.id);
  const currentIndex = versions.findIndex((v) => v.id === current.id);
  if (selectedIndex >= 0 && currentIndex >= 0) {
    return selectedIndex < currentIndex
      ? `Update to ${selected.version_number}`
      : `Downgrade to ${selected.version_number}`;
  }

  const selectedDate = Date.parse(selected.date_published);
  const currentDate = Date.parse(current.date_published);
  if (!Number.isNaN(selectedDate) && !Number.isNaN(currentDate)) {
    return selectedDate > currentDate
      ? `Update to ${selected.version_number}`
      : `Downgrade to ${selected.version_number}`;
  }

  return `Switch to ${selected.version_number}`;
}

export const changelogComponents = {
  h1: (p: ComponentProps<"h1">) => <h1 className="text-lg font-semibold text-text-primary mt-4 mb-2" {...p} />,
  h2: (p: ComponentProps<"h2">) => <h2 className="text-base font-semibold text-text-primary mt-4 mb-2" {...p} />,
  h3: (p: ComponentProps<"h3">) => <h3 className="text-sm font-semibold text-text-primary mt-3 mb-1.5" {...p} />,
  p: (p: ComponentProps<"p">) => <p className="text-sm text-text-secondary leading-relaxed my-2" {...p} />,
  a: (p: ComponentProps<"a">) => (
    <a className="text-accent hover:underline" target="_blank" rel="noreferrer noopener" {...p} />
  ),
  ul: (p: ComponentProps<"ul">) => <ul className="list-disc pl-5 my-2 text-sm text-text-secondary space-y-1" {...p} />,
  ol: (p: ComponentProps<"ol">) => <ol className="list-decimal pl-5 my-2 text-sm text-text-secondary space-y-1" {...p} />,
  code: (p: ComponentProps<"code">) => (
    <code className="px-1 py-0.5 rounded bg-surface-2 text-xs font-mono text-text-primary" {...p} />
  ),
  pre: (p: ComponentProps<"pre">) => (
    <pre className="p-3 rounded bg-surface-2 overflow-x-auto text-xs my-2" {...p} />
  ),
};

export function VersionSwitchDialog({
  open,
  onClose,
  serverId,
  mod,
  loader,
  mcVersion,
}: {
  open: boolean;
  onClose: () => void;
  serverId: string;
  mod: InstalledMod | null;
  loader: string;
  mcVersion: string;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [selectedId, setSelectedId] = useState("");
  const [searchTerm, setSearchTerm] = useState("");
  const [showIncompatible, setShowIncompatible] = useState(false);

  const projectId = mod?.source_id ?? "";
  const source = mod?.source as ModSource | undefined;

  const { data: versions = [], isFetching } = useQuery({
    queryKey: ["mod-switch-versions", serverId, source, projectId],
    queryFn: () => api.mods.getVersions(serverId, projectId, "", "", source),
    enabled: open && !!projectId && !!source,
    staleTime: 10 * 60_000,
  });

  const { data: project } = useQuery({
    queryKey: ["mod-switch-project", serverId, source, projectId],
    queryFn: () => api.mods.getProject(serverId, projectId, source),
    enabled: open && !!projectId && !!source,
    staleTime: 10 * 60_000,
  });

  useEffect(() => {
    if (!open) return;
    setSelectedId(mod?.version_id ?? "");
    setSearchTerm("");
    setShowIncompatible(false);
  }, [open, mod?.id, mod?.version_id]);

  const compatibleVersions = useMemo(
    () => versions.filter((v) => versionCompatible(v, loader, mcVersion)),
    [versions, loader, mcVersion],
  );

  const visibleVersions = useMemo(() => {
    const base = showIncompatible ? versions : compatibleVersions;
    const q = searchTerm.trim().toLowerCase();
    if (!q) return base;
    return base.filter(
      (v) =>
        v.name.toLowerCase().includes(q) ||
        v.version_number.toLowerCase().includes(q) ||
        v.game_versions.some((g) => g.toLowerCase().includes(q)),
    );
  }, [compatibleVersions, searchTerm, showIncompatible, versions]);

  useEffect(() => {
    if (!open || versions.length === 0) return;
    if (selectedId && visibleVersions.some((v) => v.id === selectedId)) return;
    const current =
      visibleVersions.find((v) => v.id === mod?.version_id) ?? visibleVersions[0];
    setSelectedId(current?.id ?? "");
  }, [open, mod?.version_id, selectedId, versions.length, visibleVersions]);

  const selected =
    versions.find((v) => v.id === selectedId) ?? visibleVersions[0] ?? null;
  const { data: selectedDetail, isFetching: loadingSelectedDetail } = useQuery({
    queryKey: ["mod-switch-version-detail", serverId, source, projectId, selected?.id],
    queryFn: () => api.mods.getVersion(serverId, projectId, selected!.id, source),
    enabled: open && !!projectId && !!source && !!selected?.id,
    staleTime: 10 * 60_000,
  });
  const selectedVersion = selectedDetail ?? selected;
  const currentVersion =
    versions.find(
      (v) =>
        (mod?.version_id && v.id === mod.version_id) ||
        v.version_number === mod?.version,
    ) ?? null;
  const selectedIsCurrent =
    !!selected &&
    ((mod?.version_id && selected.id === mod.version_id) ||
      selected.version_number === mod?.version);
  const selectedIsCompatible = selected
    ? versionCompatible(selected, loader, mcVersion)
    : true;
  const actionLabel = switchActionLabel(selectedVersion, currentVersion, versions);

  const switchMutation = useMutation({
    mutationFn: (versionId: string) => api.mods.update(serverId, mod!.id, versionId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] });
      success(`Switched ${mod?.name ?? "mod"} to ${selected?.version_number}`);
      onClose();
    },
    onError: (e: Error) => error("Switch failed", e.message),
  });

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Switch version"
      className="!max-w-6xl !p-0 overflow-hidden"
      headerClassName="mb-0 border-b border-border px-6 py-5 sm:px-8 sm:py-7"
      titleClassName="text-xl sm:text-2xl"
      closeClassName="h-11 w-11 rounded-full bg-surface-2 text-text-secondary hover:bg-surface-2/80 hover:text-text-primary"
      titleIcon={
        project?.icon_url ? (
          <img
            src={project.icon_url}
            alt=""
            className="h-14 w-14 flex-shrink-0 rounded-lg object-cover shadow-lg"
          />
        ) : (
          <span className="flex h-14 w-14 flex-shrink-0 items-center justify-center rounded-lg bg-accent/70 text-black shadow-lg">
            <Package className="h-7 w-7" />
          </span>
        )
      }
    >
      {/* Stack the version list above the changelog on phones; the fixed 21rem
          sidebar would otherwise overflow the dialog. The list gets a capped
          share of the height so the changelog stays reachable below it. */}
      <div className="grid h-[72vh] min-h-[30rem] grid-rows-[14rem_minmax(0,1fr)] md:grid-rows-none md:grid-cols-[21rem_minmax(0,1fr)]">
        <aside className="flex min-h-0 flex-col border-b border-border bg-surface-2/50 p-5 md:border-b-0 md:border-r">
          <div className="relative mb-3">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-secondary" />
            <Input
              className="pl-9"
              placeholder="Search version..."
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
            />
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto pr-1">
            {isFetching ? (
              <div className="flex justify-center py-10">
                <Loader2 className="h-5 w-5 animate-spin text-accent" />
              </div>
            ) : visibleVersions.length === 0 ? (
              <p className="px-2 py-8 text-center text-sm text-text-secondary">
                No versions found.
              </p>
            ) : (
              <div className="space-y-1">
                {visibleVersions.map((version) => {
                  const current =
                    (mod?.version_id && version.id === mod.version_id) ||
                    version.version_number === mod?.version;
                  const compatibleVersion = versionCompatible(
                    version,
                    loader,
                    mcVersion,
                  );
                  return (
                    <button
                      key={version.id}
                      type="button"
                      onClick={() => setSelectedId(version.id)}
                      className={`flex w-full items-center gap-3 rounded-md px-3 py-2 text-left transition-colors ${
                        selected?.id === version.id
                          ? "bg-accent/25 text-text-primary"
                          : "hover:bg-surface"
                      }`}
                    >
                      <span
                        className={`flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-full text-xs font-semibold ${
                          compatibleVersion
                            ? "bg-green-500/20 text-green-400"
                            : "bg-amber-500/15 text-amber-400"
                        }`}
                      >
                        {(version.version_type ?? "v").charAt(0).toUpperCase()}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-sm font-medium text-text-primary">
                          {version.version_number}
                        </span>
                        <span className="block truncate text-xs text-text-secondary">
                          {version.name || version.game_versions.join(", ")}
                        </span>
                      </span>
                      {current && (
                        <span className="rounded-full bg-surface px-2 py-0.5 text-xs text-text-secondary">
                          Current
                        </span>
                      )}
                    </button>
                  );
                })}
              </div>
            )}
          </div>

          <label className="mt-3 flex w-fit cursor-pointer select-none items-center gap-2 text-sm text-text-secondary">
            <input
              type="checkbox"
              className="accent-accent"
              checked={showIncompatible}
              onChange={(e) => setShowIncompatible(e.target.checked)}
            />
            Show incompatible
          </label>
        </aside>

        <section className="flex min-h-0 flex-col bg-surface">
          {selected ? (
            <>
              <div className="flex items-start justify-between gap-4 border-b border-border px-6 py-5">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <h3 className="truncate text-lg font-semibold text-text-primary">
                      {selectedVersion.version_number}
                    </h3>
                    {selectedVersion.version_type && (
                      <span
                        className={`rounded-full border px-2 py-0.5 text-xs capitalize ${versionTypeClass(
                          selectedVersion.version_type,
                        )}`}
                      >
                        {selectedVersion.version_type}
                      </span>
                    )}
                    {selectedIsCurrent && (
                      <span className="rounded-full border border-border bg-surface-2 px-2 py-0.5 text-xs text-text-secondary">
                        Current
                      </span>
                    )}
                  </div>
                  <p className="mt-1 flex flex-wrap items-center gap-2 text-sm text-text-secondary">
                    <FileText className="h-4 w-4" />
                    Changelog
                    {selectedVersion.loaders.length > 0 && (
                      <>
                        <span>•</span>
                        <span>{selectedVersion.loaders.join(", ")}</span>
                      </>
                    )}
                    {selectedVersion.game_versions.length > 0 && (
                      <>
                        <span>•</span>
                        <span>{selectedVersion.game_versions.join(", ")}</span>
                      </>
                    )}
                  </p>
                </div>
                {selectedVersion.date_published && (
                  <span className="flex-shrink-0 text-sm text-text-secondary">
                    {formatVersionDate(selectedVersion.date_published)}
                  </span>
                )}
              </div>

              <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">
                {loadingSelectedDetail ? (
                  <div className="flex justify-center py-10">
                    <Loader2 className="h-5 w-5 animate-spin text-accent" />
                  </div>
                ) : selectedVersion.changelog ? (
                  <ReactMarkdown
                    remarkPlugins={[remarkGfm]}
                    components={changelogComponents}
                  >
                    {selectedVersion.changelog}
                  </ReactMarkdown>
                ) : (
                  <p className="text-sm text-text-secondary">
                    No changelog provided for this version.
                  </p>
                )}
              </div>
            </>
          ) : (
            <div className="flex flex-1 items-center justify-center text-sm text-text-secondary">
              Select a version
            </div>
          )}

          {/* Wrap on phones so the long action label ("Downgrade to …") drops
              below the warning rather than overflowing the footer. */}
          <div className="flex flex-wrap items-center gap-3 border-t border-border bg-surface-2/40 px-4 py-4 sm:px-6">
            <div className="flex min-w-0 flex-1 basis-full items-center gap-2 text-sm text-amber-400 sm:basis-0">
              <AlertTriangle className="h-5 w-5 flex-shrink-0" />
              <span className="truncate">
                Updating can break your instance. Review version changelogs and back up first.
              </span>
            </div>
            <Button variant="outline" onClick={onClose} disabled={switchMutation.isPending}>
              <X className="h-4 w-4" />
              Cancel
            </Button>
            <Button
              onClick={() => selected && switchMutation.mutate(selected.id)}
              loading={switchMutation.isPending}
              disabled={!selected || selectedIsCurrent}
              title={
                selectedIsCompatible
                  ? "Switch version"
                  : "Switch to an incompatible version"
              }
            >
              {!switchMutation.isPending && <Download className="h-4 w-4" />}
              {switchMutation.isPending ? "Downloading…" : actionLabel}
            </Button>
          </div>
        </section>
      </div>
    </Dialog>
  );
}
