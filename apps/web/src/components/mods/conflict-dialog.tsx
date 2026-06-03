import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ChevronDown, ChevronRight } from "lucide-react";
import { Dialog } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { ModConflict } from "@/lib/types";

// ModConflictDialog surfaces a Fabric incompatible-mods failure the agent
// detected while starting the server. Each suggestion names a mod that can be
// disabled (its jar renamed to .disabled) to break the conflict; the user picks
// which to disable, then optionally restarts.
export function ModConflictDialog({
  serverId,
  conflict,
  onClose,
}: {
  serverId: string;
  conflict: ModConflict;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [showLog, setShowLog] = useState(false);

  // De-dupe mod ids across suggestions; default every conflicting mod selected.
  const modIds = Array.from(
    new Set(conflict.suggestions.map((s) => s.mod_id).filter(Boolean)),
  );
  const [selected, setSelected] = useState<Set<string>>(() => new Set(modIds));

  const toggle = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const apply = useMutation({
    mutationFn: async (restart: boolean) => {
      const ids = [...selected];
      const res = await api.mods.disableConflict(serverId, ids);
      if (restart) await api.servers.start(serverId);
      return res;
    },
    onSuccess: (res, restart) => {
      qc.invalidateQueries({ queryKey: ["agent-status", serverId] });
      qc.invalidateQueries({ queryKey: ["server", serverId] });
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      const n = res.disabled.length;
      success(
        n > 0 ? `Disabled ${n} mod${n !== 1 ? "s" : ""}` : "No matching jars",
        restart ? "Restarting server…" : res.disabled.join(", ") || undefined,
      );
      onClose();
    },
    onError: (e: Error) => error("Disable failed", e.message),
  });

  return (
    <Dialog
      open
      onClose={onClose}
      title="Incompatible mods detected"
      description={conflict.summary}
      className="max-w-xl"
    >
      <div className="flex items-start gap-2 rounded-md border border-yellow-800/50 bg-yellow-900/20 p-3 text-sm text-yellow-300">
        <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" />
        <p>
          The server stopped because Fabric found mods that can't run together.
          Disable one or more of the conflicting mods, then restart.
        </p>
      </div>

      <div className="mt-4 space-y-2">
        {conflict.suggestions.length === 0 && (
          <p className="text-sm text-text-secondary">
            Couldn't parse individual mods — see the raw log below.
          </p>
        )}
        {conflict.suggestions.map((s, i) => (
          <label
            key={`${s.mod_id}-${i}`}
            className="flex cursor-pointer items-start gap-3 rounded-md border border-border bg-surface-2 px-3 py-2.5"
          >
            <input
              type="checkbox"
              className="mt-1"
              checked={selected.has(s.mod_id)}
              onChange={() => toggle(s.mod_id)}
            />
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="text-sm font-medium text-text-primary">
                  {s.mod_name}
                </span>
                <Badge variant="muted" className="font-mono">
                  {s.mod_id}
                </Badge>
                {s.version && (
                  <span className="font-mono text-xs text-text-secondary">
                    {s.version}
                  </span>
                )}
              </div>
              {s.requirements && s.requirements.length > 0 && (
                <p className="mt-1 text-xs text-text-secondary">
                  Needs: {s.requirements.join("; ")}
                </p>
              )}
            </div>
          </label>
        ))}
      </div>

      {conflict.raw.length > 0 && (
        <div className="mt-3">
          <button
            onClick={() => setShowLog((v) => !v)}
            className="flex items-center gap-1 text-xs text-text-secondary hover:text-text-primary"
          >
            {showLog ? (
              <ChevronDown className="h-3.5 w-3.5" />
            ) : (
              <ChevronRight className="h-3.5 w-3.5" />
            )}
            Raw log
          </button>
          {showLog && (
            <pre className="mt-2 max-h-48 overflow-auto rounded-md border border-border bg-[#0f0f0f] p-3 font-mono text-[11px] leading-4 text-text-secondary">
              {conflict.raw.join("\n")}
            </pre>
          )}
        </div>
      )}

      <div className="mt-5 flex justify-end gap-3">
        <Button variant="outline" onClick={onClose} disabled={apply.isPending}>
          Dismiss
        </Button>
        <Button
          variant="outline"
          onClick={() => apply.mutate(false)}
          loading={apply.isPending}
          disabled={selected.size === 0}
        >
          Disable selected
        </Button>
        <Button
          onClick={() => apply.mutate(true)}
          loading={apply.isPending}
          disabled={selected.size === 0}
        >
          Disable &amp; restart
        </Button>
      </div>
    </Dialog>
  );
}
