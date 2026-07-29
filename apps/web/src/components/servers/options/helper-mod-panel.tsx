import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plug } from "lucide-react";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { Server } from "@/lib/types";

/**
 * Enables the manager's own helper mod for a server.
 *
 * The jar ships inside the agent binary, so turning this on is the entire
 * install: no download, no upload, no file management. The agent reconciles the
 * jar in the server's mods folder on the next start, and turning it off deletes
 * it again.
 */
export function HelperModPanel({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();

  const { data, isLoading } = useQuery({
    queryKey: ["server", server.id, "helper-mod"],
    queryFn: () => api.servers.helperMod(server.id),
  });

  const mutation = useMutation({
    mutationFn: (enabled: boolean) => api.servers.setHelperMod(server.id, enabled),
    onSuccess: (result) => {
      qc.invalidateQueries({ queryKey: ["server", server.id, "helper-mod"] });
      success(
        result.enabled ? "Helper mod enabled" : "Helper mod disabled",
        "Takes effect the next time this server starts.",
      );
    },
    onError: (e: Error) => error("Could not change the helper mod", e.message),
  });

  const supported = data?.supported ?? false;
  const enabled = data?.enabled ?? false;

  return (
    <section className="overflow-hidden rounded-lg border border-border bg-surface">
      <div className="flex items-center gap-2.5 border-b border-border bg-surface-2/40 px-3.5 py-2.5">
        <div className="grid h-7 w-7 flex-shrink-0 place-items-center rounded-md border border-accent/20 bg-accent/10 text-accent">
          <Plug className="h-4 w-4" />
        </div>
        <h2 className="text-sm font-semibold text-text-primary">Helper Mod</h2>
        <span className="truncate text-xs text-text-secondary">
          — richer stats and reliable actions
        </span>
      </div>

      <div className="space-y-3 p-3.5">
        <label
          className={
            supported && !isLoading
              ? "flex cursor-pointer items-start gap-2.5"
              : "flex items-start gap-2.5 opacity-60"
          }
        >
          <input
            type="checkbox"
            className="mt-0.5"
            checked={enabled}
            disabled={!supported || isLoading || mutation.isPending}
            onChange={(e) => mutation.mutate(e.target.checked)}
          />
          <span>
            <span className="block text-sm text-text-primary">
              Install the helper mod on this server
            </span>
            <span className="block text-xs text-text-secondary">
              Ships with the manager — nothing to download. Adds live TPS and
              tick times, memory and chunk/entity counts, and exact join and
              leave tracking. Console commands report what they actually
              returned instead of being fired blindly.
            </span>
          </span>
        </label>

        {!isLoading && !supported && data?.platform !== "fabric" && (
          <p className="text-xs text-text-secondary">
            Only Fabric servers are supported right now
            {data?.platform ? ` (this one runs ${data.platform})` : ""}. Other
            platforms keep using log reading, which still works — it is just
            less precise.
          </p>
        )}

        {!isLoading && !supported && data?.platform === "fabric" && (
          <p className="text-xs text-text-secondary">
            This build of the mod is for Minecraft {data.required_mc_series}, and
            this server runs {data.mc_version || "an unrecorded version"}.
            Fabric refuses to start a server whose mods declare a different
            version, so the panel will not install it here.
          </p>
        )}

        {supported && (
          <p className="text-xs text-text-secondary">
            The mod does nothing unless this panel starts the server, and it is
            managed for you: it will not appear as a mod you have to update, and
            turning this off removes the jar. Changes apply on the next restart.
          </p>
        )}
      </div>
    </section>
  );
}
