import { useState } from "react";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import {
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  Loader2,
  Package,
} from "lucide-react";
import { Dialog } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { ConflictSuggestion, ModConflict } from "@/lib/types";

// ModConflictDialog surfaces a startup failure the agent detected while
// starting the server: a Fabric incompatible-mods block, a mod that crashed
// startup (e.g. a broken mixin), a Java version mismatch, or a required
// dependency that isn't installed. Suggestions carry the fix — install the
// named mod, or disable it (its jar renamed to .disabled) — and the dialog
// offers to apply it and restart in one step.
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

  const isJava = conflict.kind === "java_version";
  const isCrash = conflict.kind === "crash";
  const isMissingDep = conflict.kind === "missing_dependency";
  // Older agents send null instead of [] when nothing was parsed.
  const suggestions = conflict.suggestions ?? [];
  const rawLog = conflict.raw ?? [];
  // A suggestion is fixed either by installing the named mod (a dependency the
  // server needs but doesn't have) or by disabling it (a mod that clashes or
  // crashes). Split on the action rather than the conflict kind so a block that
  // mixes both — Fabric can emit one — offers both fixes.
  const installs = suggestions.filter((s) => s.action === "install");
  const disables = suggestions.filter((s) => s.action !== "install");

  const title = isJava
    ? "Server needs a newer version of Java"
    : isMissingDep
      ? "Missing required dependency"
      : isCrash
        ? "A mod crashed the server"
        : "Incompatible mods detected";
  const banner = isJava
    ? "This Minecraft build was compiled for a newer version of Java than the one used to launch it. No mods are at fault — switch this server to the required Java version, then restart."
    : isMissingDep
      ? "The server can't start because one or more installed mods need another mod that isn't there. Install the missing dependency below, then restart."
      : isCrash
        ? "The server crashed on startup because a mod failed to load (often a broken or outdated mixin). Disable the mod below, then restart."
        : "The server stopped because Fabric found mods that can't run together. Disable one or more of the conflicting mods, then restart.";

  // De-dupe mod ids across suggestions; default every conflicting mod selected.
  const modIds = Array.from(
    new Set(disables.map((s) => s.mod_id).filter(Boolean)),
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
      title={title}
      description={conflict.summary}
      className="max-w-xl"
    >
      <div className="flex items-start gap-2 rounded-md border border-yellow-800/50 bg-yellow-900/20 p-3 text-sm text-yellow-300">
        <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" />
        <p>{banner}</p>
      </div>

      {isJava && (
        <JavaFix
          serverId={serverId}
          required={conflict.required_java ?? 0}
          onClose={onClose}
        />
      )}

      {installs.length > 0 && (
        <MissingDepsFix
          serverId={serverId}
          suggestions={installs}
          onClose={onClose}
        />
      )}

      {!isJava && (
      <div className="mt-4 space-y-2">
        {suggestions.length === 0 && (
          <p className="text-sm text-text-secondary">
            Couldn't parse individual mods — see the raw log below.
          </p>
        )}
        {disables.length > 0 && installs.length > 0 && (
          <p className="pt-2 text-sm font-medium text-text-primary">
            Mods to disable
          </p>
        )}
        {disables.map((s, i) => (
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
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span className="break-words text-sm font-medium text-text-primary">
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
      )}

      {rawLog.length > 0 && (
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
              {rawLog.join("\n")}
            </pre>
          )}
        </div>
      )}

      <div className="mt-5 flex flex-wrap justify-end gap-3">
        <Button variant="outline" onClick={onClose} disabled={apply.isPending}>
          {isJava || isMissingDep ? "Close" : "Dismiss"}
        </Button>
        {!isJava && disables.length > 0 && (
          <>
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
          </>
        )}
      </div>
    </Dialog>
  );
}

// MissingDepsFix is the body of the dialog when the loader named a mod the
// server needs but doesn't have — nearly always Fabric API. The loader reports
// it as a bare mod id ("fabric"), which is not something the operator can act
// on, so this resolves each id to a real Modrinth project (pinned to a build
// that fits this server's Minecraft version) and offers to install and restart.
function MissingDepsFix({
  serverId,
  suggestions,
  onClose,
}: {
  serverId: string;
  suggestions: ConflictSuggestion[];
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();

  const modIds = Array.from(
    new Set(suggestions.map((s) => s.mod_id).filter(Boolean)),
  );

  const { data, isLoading, isError } = useQuery({
    queryKey: ["missing-deps", serverId, modIds.join(",")],
    queryFn: () => api.mods.resolveMissing(serverId, modIds),
    staleTime: 30_000,
  });

  // Only entries with a pinned version can be installed; the rest carry an
  // explanation (no build for this Minecraft version, already installed, …).
  const installable = (data ?? []).filter((d) => d.found && d.version_id);

  const install = useMutation({
    mutationFn: async (restart: boolean) => {
      // Sequential, not concurrent: each install uploads a jar through the
      // agent and records rows, and a failure part-way should stop rather than
      // race the others.
      const done: string[] = [];
      for (const dep of installable) {
        await api.mods.install(
          serverId,
          "modrinth",
          dep.project_id!,
          dep.version_id!,
          true,
        );
        done.push(dep.title || dep.mod_id);
      }
      if (restart) await api.servers.start(serverId);
      return done;
    },
    onSuccess: (done, restart) => {
      qc.invalidateQueries({ queryKey: ["agent-status", serverId] });
      qc.invalidateQueries({ queryKey: ["server", serverId] });
      qc.invalidateQueries({ queryKey: ["mods", serverId] });
      success(
        `Installed ${done.join(", ")}`,
        restart ? "Restarting server…" : undefined,
      );
      onClose();
    },
    onError: (e: Error) => error("Install failed", e.message),
  });

  return (
    <div className="mt-4 space-y-3">
      {isLoading && (
        <p className="flex items-center gap-2 text-sm text-text-secondary">
          <Loader2 className="h-4 w-4 animate-spin text-accent" />
          Looking up the missing {modIds.length === 1 ? "mod" : "mods"}…
        </p>
      )}

      {isError && (
        <p className="text-sm text-text-secondary">
          Couldn't reach Modrinth to look up{" "}
          {modIds.map((m) => `"${m}"`).join(", ")}. Install{" "}
          {modIds.length === 1 ? "it" : "them"} from the Mods tab, then restart.
        </p>
      )}

      {(data ?? []).map((dep) => {
        const suggestion = suggestions.find((s) => s.mod_id === dep.mod_id);
        return (
          <div
            key={dep.mod_id}
            className="flex items-start gap-3 rounded-md border border-border bg-surface-2 px-3 py-2.5"
          >
            {dep.icon_url ? (
              <img
                src={dep.icon_url}
                alt=""
                className="mt-0.5 h-9 w-9 flex-shrink-0 rounded"
              />
            ) : (
              <Package className="mt-0.5 h-9 w-9 flex-shrink-0 text-text-secondary" />
            )}
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span className="break-words text-sm font-medium text-text-primary">
                  {dep.title || dep.mod_id}
                </span>
                <Badge variant="muted" className="font-mono">
                  {dep.mod_id}
                </Badge>
                {dep.version_number && (
                  <span className="font-mono text-xs text-text-secondary">
                    {dep.version_number}
                  </span>
                )}
              </div>
              {suggestion?.required_by && suggestion.required_by.length > 0 && (
                <p className="mt-1 text-xs text-text-secondary">
                  Required by: {suggestion.required_by.join(", ")}
                </p>
              )}
              {dep.error && (
                <p className="mt-1 text-xs text-yellow-400">{dep.error}</p>
              )}
            </div>
          </div>
        );
      })}

      {data && installable.length > 0 && (
        <div className="flex flex-wrap justify-end gap-3 pt-1">
          <Button
            variant="outline"
            onClick={() => install.mutate(false)}
            loading={install.isPending}
          >
            Install only
          </Button>
          <Button
            onClick={() => install.mutate(true)}
            loading={install.isPending}
          >
            Install &amp; restart
          </Button>
        </div>
      )}

      {data && data.length > 0 && installable.length === 0 && (
        <p className="text-sm text-text-secondary">
          Nothing here can be installed automatically — see the reason on each
          entry above, and the raw log below.
        </p>
      )}
    </div>
  );
}

// JavaFix is the body of the dialog for a "java_version" conflict: it lists the
// Java runtimes installed on the server's node and, if any is new enough, offers
// a one-click switch (repoint java_binary + restart). If none qualifies, it
// shows OS-appropriate install instructions for the required version.
function JavaFix({
  serverId,
  required,
  onClose,
}: {
  serverId: string;
  required: number;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();

  const { data, isLoading, isError } = useQuery({
    queryKey: ["java-installations", serverId],
    queryFn: () => api.servers.javaInstallations(serverId),
    staleTime: 30_000,
  });

  const switchJava = useMutation({
    mutationFn: async (path: string) => {
      await api.servers.update(serverId, { java_binary: path });
      await api.servers.start(serverId);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["agent-status", serverId] });
      qc.invalidateQueries({ queryKey: ["server", serverId] });
      success("Java runtime switched", "Restarting server…");
      onClose();
    },
    onError: (e: Error) => error("Couldn't switch Java", e.message),
  });

  // Smallest sufficient version first — prefer the closest match to required.
  const compatible = (data?.installations ?? [])
    .filter((j) => required > 0 && j.major >= required)
    .sort((a, b) => a.major - b.major);

  return (
    <div className="mt-4 space-y-3">
      {isLoading && (
        <p className="flex items-center gap-2 text-sm text-text-secondary">
          <Loader2 className="h-4 w-4 animate-spin text-accent" />
          Checking installed Java versions…
        </p>
      )}

      {isError && (
        <p className="text-sm text-text-secondary">
          Couldn't reach the node to list installed Java versions. Install Java{" "}
          {required > 0 ? `${required} or newer` : "a newer version"} and set
          its path in the server's "Java Binary" option.
        </p>
      )}

      {data && compatible.length > 0 && (
        <>
          <p className="text-sm text-text-secondary">
            {compatible.length === 1
              ? "A compatible Java runtime is installed on this node:"
              : "Compatible Java runtimes are installed on this node:"}
          </p>
          <div className="space-y-2">
            {compatible.map((j) => (
              <div
                key={j.path}
                className="flex items-center gap-3 rounded-md border border-border bg-surface-2 px-3 py-2.5"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-text-primary">
                      Java {j.major}
                    </span>
                    <Badge variant="muted" className="font-mono">
                      {j.version}
                    </Badge>
                  </div>
                  <p className="mt-0.5 truncate font-mono text-xs text-text-secondary">
                    {j.path}
                  </p>
                </div>
                <Button
                  onClick={() => switchJava.mutate(j.path)}
                  loading={switchJava.isPending}
                >
                  Use &amp; restart
                </Button>
              </div>
            ))}
          </div>
        </>
      )}

      {data && compatible.length === 0 && (
        <>
          <p className="text-sm text-text-secondary">
            No Java new enough is installed on this node
            {required > 0 ? ` (need Java ${required} or newer)` : ""}. Install
            one of these, then reopen this dialog (or set its path in the
            server's "Java Binary" option):
          </p>
          <JavaInstallHints os={data.os} major={required} />
        </>
      )}
    </div>
  );
}

// JavaInstallHints renders copy-paste install commands for the host OS plus a
// download link, defaulting the version to 21 (current LTS) when unknown.
function JavaInstallHints({ os, major }: { os: string; major: number }) {
  const v = major > 0 ? major : 21;
  const hints: { label: string; cmd: string }[] = [];
  if (os === "windows") {
    hints.push({
      label: "winget",
      cmd: `winget install EclipseAdoptium.Temurin.${v}.JDK`,
    });
  } else if (os === "darwin") {
    hints.push({ label: "Homebrew", cmd: `brew install openjdk@${v}` });
  } else if (os === "linux") {
    hints.push({
      label: "Debian / Ubuntu",
      cmd: `sudo apt install openjdk-${v}-jdk`,
    });
    hints.push({
      label: "Fedora / RHEL",
      cmd: `sudo dnf install java-${v}-openjdk-devel`,
    });
    hints.push({ label: "Arch", cmd: `sudo pacman -S jdk${v}-openjdk` });
  }

  return (
    <div className="space-y-2">
      {hints.map((h) => (
        <div
          key={h.label}
          className="rounded-md border border-border bg-surface-2 px-3 py-2"
        >
          <div className="text-xs font-medium text-text-secondary">
            {h.label}
          </div>
          <CopyableCommand cmd={h.cmd} />
        </div>
      ))}
      <a
        href={`https://adoptium.net/temurin/releases/?version=${v}`}
        target="_blank"
        rel="noreferrer noopener"
        className="inline-flex items-center gap-1 text-sm text-accent hover:underline"
      >
        Or download Eclipse Temurin {v}
      </a>
    </div>
  );
}

function CopyableCommand({ cmd }: { cmd: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="mt-1 flex items-center gap-2">
      <code className="flex-1 overflow-x-auto rounded bg-[#0f0f0f] px-2 py-1 font-mono text-xs text-text-primary">
        {cmd}
      </code>
      <button
        onClick={() => {
          navigator.clipboard?.writeText(cmd);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }}
        className="shrink-0 text-xs text-text-secondary hover:text-text-primary"
      >
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}
