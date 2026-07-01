import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Package,
  Trash2,
  Pin,
  PinOff,
  ArrowUpCircle,
  Unlink,
  ArrowRightLeft,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type {
  InstalledMod,
  ModUpdate,
} from "@/lib/types";
import { environmentTag, sourceBadgeClass } from "./shared";

export function Switch({
  checked,
  onChange,
  disabled,
  title,
  "aria-label": ariaLabel,
}: {
  checked: boolean;
  onChange: () => void;
  disabled?: boolean;
  title?: string;
  "aria-label"?: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={ariaLabel}
      disabled={disabled}
      onClick={onChange}
      title={title}
      className={`relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors disabled:opacity-50 disabled:cursor-not-allowed ${
        checked ? "bg-accent" : "bg-surface-2 border border-border"
      }`}
    >
      <span
        className={`inline-block h-3.5 w-3.5 transform rounded-full transition-transform ${
          checked ? "translate-x-[1.125rem] bg-black" : "translate-x-1 bg-text-secondary"
        }`}
      />
    </button>
  );
}

export function InstalledModRow({
  mod,
  serverId,
  update,
  onUninstall,
  onShowDetails,
  onSwitchVersion,
}: {
  mod: InstalledMod;
  serverId: string;
  update?: ModUpdate;
  onUninstall: () => void;
  onShowDetails?: () => void;
  onSwitchVersion?: () => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();

  const projectQuery = useQuery({
    queryKey: ["mod-project", mod.source, mod.source_id],
    queryFn: () =>
      api.mods.getProject(mod.server_id, mod.source_id!, mod.source),
    enabled: !!mod.source_id,
    staleTime: 10 * 60_000,
  });
  const project = projectQuery.data;
  const envTag = environmentTag(project?.client_side, project?.server_side);

  const updateMutation = useMutation({
    mutationFn: () => api.mods.update(serverId, mod.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] });
      success(`Updated ${mod.name}`);
    },
    onError: (e: Error) => error("Update failed", e.message),
  });

  const pinMutation = useMutation({
    mutationFn: () => api.mods.pin(serverId, mod.id, !mod.pinned),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      qc.invalidateQueries({ queryKey: ["mod-updates", serverId] });
    },
    onError: (e: Error) => error("Pin failed", e.message),
  });

  const enabledMutation = useMutation({
    mutationFn: () => api.mods.setEnabled(serverId, mod.id, !mod.enabled),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      success(mod.enabled ? `Disabled ${mod.name}` : `Enabled ${mod.name}`);
    },
    onError: (e: Error) => error("Toggle failed", e.message),
  });

  return (
    <div
      className={`grid grid-cols-1 gap-2 xl:grid-cols-[minmax(0,1fr)_minmax(0,18rem)_auto] xl:items-center xl:gap-4 px-4 py-3 xl:py-2.5 hover:bg-surface-2/40 border-b border-border/50 ${
        mod.enabled ? "" : "opacity-60"
      }`}
    >
      {/* Project */}
      <div className="flex items-center gap-3 min-w-0">
        {project?.icon_url ? (
          <img
            src={project.icon_url}
            alt=""
            className="h-10 w-10 rounded-md flex-shrink-0 object-cover"
          />
        ) : (
          <div className="h-10 w-10 rounded-md bg-surface-2 flex items-center justify-center flex-shrink-0">
            <Package className="h-4 w-4 text-text-secondary" />
          </div>
        )}
        <div className="min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            {onShowDetails && mod.source_id ? (
              <button
                onClick={onShowDetails}
                className="text-sm font-medium text-text-primary truncate hover:text-accent transition-colors text-left"
                title="View details"
              >
                {project?.title ?? mod.name}
              </button>
            ) : (
              <p className="text-sm font-medium text-text-primary truncate">
                {project?.title ?? mod.name}
              </p>
            )}
            {!mod.enabled && (
              <span className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-surface-2 text-text-secondary border border-border/50">
                disabled
              </span>
            )}
            <span
              className={`text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded border ${sourceBadgeClass(mod.source)}`}
              title={
                mod.source === "custom"
                  ? "Uploaded manually"
                  : `Installed from ${mod.source}`
              }
            >
              {mod.source}
            </span>
            {envTag && (
              <span
                className={`text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded border ${envTag.className}`}
                title={envTag.title}
              >
                {envTag.label}
              </span>
            )}
            {mod.installed_as_dep && (
              <span
                className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-surface-2 text-text-secondary border border-border/50"
                title={
                  mod.required_by.length > 0
                    ? `Required by ${mod.required_by.join(", ")}`
                    : "Installed automatically as a dependency"
                }
              >
                dependency
              </span>
            )}
            {mod.orphaned && (
              <span
                className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-red-500/15 text-red-400 border border-red-500/30"
                title="Auto-installed as a dependency, but nothing requires it anymore — safe to remove."
              >
                <Unlink className="h-3 w-3" />
                not needed
              </span>
            )}
            {mod.pinned && (
              <span className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-400 border border-amber-500/30">
                pinned
              </span>
            )}
          </div>
          {mod.required_by.length > 0 && (
            <p className="text-xs text-text-secondary mt-0.5 truncate">
              Required by {mod.required_by.join(", ")}
            </p>
          )}
        </div>
      </div>

      {/* Version. On phones this stacks under the title as its own line rather
          than being dropped, so the row reads as a card instead of a cramped
          strip of controls. */}
      <div className="min-w-0">
        <p className="text-sm text-text-primary truncate">{mod.version}</p>
        <p className="text-xs text-text-secondary truncate">
          {mod.install_path}/{mod.file_name}
        </p>
        {update && (
          <p className="text-xs text-green-400 truncate">
            ↑ {update.latest_version}
          </p>
        )}
      </div>

      {/* Actions. While stacked these sit on their own row with a divider and
          right alignment so the tap targets aren't squeezed against the title.
          They only rejoin the row once the 3-column grid engages at xl. */}
      <div className="flex items-center gap-1 flex-shrink-0 justify-end border-t border-border/40 pt-2 xl:border-t-0 xl:pt-0">
        {onSwitchVersion && (
          <Button
            size="sm"
            variant="ghost"
            onClick={onSwitchVersion}
            title="Switch version"
            aria-label={`Switch version of ${mod.name}`}
          >
            <ArrowRightLeft className="h-3.5 w-3.5 text-text-secondary" />
          </Button>
        )}
        {update && (
          <Button
            size="sm"
            variant="outline"
            onClick={() => updateMutation.mutate()}
            loading={updateMutation.isPending}
            title={`Update to ${update.latest_version}`}
            aria-label={`Update ${mod.name} to ${update.latest_version}`}
          >
            <ArrowUpCircle className="h-3.5 w-3.5" />
          </Button>
        )}
        <Switch
          checked={mod.enabled}
          onChange={() => enabledMutation.mutate()}
          disabled={enabledMutation.isPending}
          title={mod.enabled ? "Disable (keep file)" : "Enable"}
          aria-label={
            mod.enabled ? `Disable ${mod.name}` : `Enable ${mod.name}`
          }
        />
        {mod.source_id && (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => pinMutation.mutate()}
            loading={pinMutation.isPending}
            title={mod.pinned ? "Unpin (allow updates)" : "Pin (skip updates)"}
            aria-label={
              mod.pinned
                ? `Unpin ${mod.name} (allow updates)`
                : `Pin ${mod.name} (skip updates)`
            }
          >
            {mod.pinned ? (
              <PinOff className="h-3.5 w-3.5 text-amber-400" />
            ) : (
              <Pin className="h-3.5 w-3.5 text-text-secondary" />
            )}
          </Button>
        )}
        <Button
          size="sm"
          variant="ghost"
          onClick={onUninstall}
          title="Uninstall"
          aria-label={`Uninstall ${mod.name}`}
        >
          <Trash2 className="h-3.5 w-3.5 text-red-400" />
        </Button>
      </div>
    </div>
  );
}
