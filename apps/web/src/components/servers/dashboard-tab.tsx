import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ArrowRight,
  FolderTree,
  HardDrive,
  Loader2,
  Save,
  SendHorizontal,
  ShieldCheck,
  SlidersHorizontal,
  Terminal,
  UserPlus,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ResourceChart } from "@/components/charts/resource-chart";
import { SafeUpdateDialog } from "@/components/mods/safe-update-dialog";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type {
  Server,
  Backup,
  ModUpdateRun,
  ServerPermission,
} from "@/lib/types";
import { Panel, StatTile, type ServerSection } from "./shared";

// Mirrors the validation the Players panel uses: a plain Java name. The agent
// re-validates, so this just guards obvious typos before the request.
const NAME_RE = /^[A-Za-z0-9_]{1,16}$/;

// A focused, single-purpose capture for the whitelist quick action — just a name
// and submit. For operator/ban or Bedrock-prefixed names, the Players tab's
// fuller dialog still applies.
function WhitelistQuickAddDialog({
  open,
  onClose,
  onSubmit,
  busy,
  serverOnline,
}: {
  open: boolean;
  onClose: () => void;
  onSubmit: (name: string) => void;
  busy: boolean;
  serverOnline: boolean;
}) {
  const [name, setName] = useState("");
  const trimmed = name.trim();
  const valid = NAME_RE.test(trimmed);

  useEffect(() => {
    if (open) setName("");
  }, [open]);

  const submit = () => {
    if (valid) onSubmit(trimmed);
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Add to whitelist"
      description={
        serverOnline
          ? "Applied live with /whitelist add."
          : "Written to whitelist.json — takes effect on next start."
      }
      titleIcon={<UserPlus className="h-5 w-5 text-accent" />}
    >
      <label className="mb-1 block text-xs font-medium text-text-secondary">
        Player name
      </label>
      <Input
        placeholder="e.g. Notch"
        value={name}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") submit();
        }}
        autoFocus
      />
      {trimmed && !valid && (
        <p className="mt-1 text-xs text-red-400">
          Names are 1–16 characters: letters, digits, underscore.
        </p>
      )}
      <div className="mt-6 flex justify-end gap-2">
        <Button variant="outline" onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button disabled={!valid || busy} loading={busy} onClick={submit}>
          <UserPlus className="h-4 w-4" /> Add to whitelist
        </Button>
      </div>
    </Dialog>
  );
}

// A uniform quick-action button: outline style, left-aligned icon + label, with
// the label truncating so longer actions don't overflow a narrow grid cell.
function QuickButton({
  icon,
  label,
  onClick,
  loading,
}: {
  icon: ReactNode;
  label: string;
  onClick: () => void;
  loading?: boolean;
}) {
  return (
    <Button
      variant="outline"
      className="w-full min-w-0 justify-start"
      onClick={onClick}
      loading={loading}
    >
      <span className="flex-shrink-0">{icon}</span>
      <span className="truncate">{label}</span>
    </Button>
  );
}

const RUN_PHASE_LABEL: Record<string, string> = {
  checking: "Checking for updates…",
  applying: "Applying updates…",
  verifying: "Restarting & verifying boot…",
  isolating: "Isolating a bad update…",
  reverting: "Reverting…",
  restoring: "Restoring backup…",
  done: "Finishing up…",
};

// Coarse, monotonic fill per phase so the bar always moves forward through a
// run. The per-mod step statuses only flip at the very end, so they can't drive
// the bar; the time-based 45s "stays up" wait instead surfaces as a live
// countdown the engine writes into detail.message.
const PHASE_FRACTION: Record<string, number> = {
  checking: 0.12,
  applying: 0.45,
  verifying: 0.75,
  isolating: 0.85,
  reverting: 0.85,
  restoring: 0.92,
  done: 1,
};

// Live progress for an in-flight safe update, driven by the run record the Mods
// tab also polls.
function SafeUpdateProgress({ run }: { run: ModUpdateRun }) {
  const phase = run.detail?.phase ?? "checking";
  const pct = Math.round((PHASE_FRACTION[phase] ?? 0.1) * 100);

  return (
    <div className="rounded-md border border-border bg-surface-2/50 p-3">
      <div className="mb-1.5 flex items-center gap-2 text-sm">
        <Loader2 className="h-4 w-4 flex-shrink-0 animate-spin text-accent" />
        <span className="min-w-0 flex-1 truncate text-text-primary">
          {run.detail?.message || RUN_PHASE_LABEL[phase] || "Working…"}
        </span>
        <span className="flex-shrink-0 font-mono text-xs text-text-secondary">
          {pct}%
        </span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface">
        <div
          className="h-full rounded-full bg-accent transition-all duration-500"
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  );
}

// A one-shot console command run against the live server, without leaving the
// dashboard. The full console (with output stream) lives in the Console tab.
function SendCommandDialog({
  open,
  onClose,
  onSubmit,
  busy,
}: {
  open: boolean;
  onClose: () => void;
  onSubmit: (command: string) => void;
  busy: boolean;
}) {
  const [command, setCommand] = useState("");
  const trimmed = command.trim();

  useEffect(() => {
    if (open) setCommand("");
  }, [open]);

  const submit = () => {
    if (trimmed) onSubmit(trimmed);
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Send command"
      description="Runs once on the live server. Open the Console tab to see output."
      titleIcon={<Terminal className="h-5 w-5 text-accent" />}
    >
      <label className="mb-1 block text-xs font-medium text-text-secondary">
        Command
      </label>
      <div className="flex items-center gap-2">
        <span className="font-mono text-sm text-text-secondary">/</span>
        <Input
          placeholder="say Hello everyone"
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") submit();
          }}
          className="font-mono"
          autoFocus
        />
      </div>
      <p className="mt-1 text-xs text-text-secondary">
        Enter the command without the leading slash.
      </p>
      <div className="mt-6 flex justify-end gap-2">
        <Button variant="outline" onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button disabled={!trimmed || busy} loading={busy} onClick={submit}>
          <SendHorizontal className="h-4 w-4" /> Send
        </Button>
      </div>
    </Dialog>
  );
}

export function DashboardTab({
  server,
  backups,
  can,
  onSection,
}: {
  server: Server;
  backups: Backup[];
  can: (permission: ServerPermission) => boolean;
  onSection: (section: ServerSection) => void;
}) {
  const { success, error } = useNotifications();
  const qc = useQueryClient();
  const [whitelistOpen, setWhitelistOpen] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false);
  const [showSafeUpdate, setShowSafeUpdate] = useState(false);

  const isOnline = server.status === "online";
  const canWhitelist = can("players.whitelist");
  const canSafeUpdate = can("mods.update");
  const canBackup = can("backups.create");
  const canConsole = can("console");

  const latestBackup = backups[0];
  // A backup runs as a detached job on the agent, so the create call returns
  // immediately; reflect the running record (polled by the parent) to keep the
  // button busy until the zip actually finishes.
  const runningBackup = backups.some((b) => b.status === "running");
  const ram =
    server.ram_mb_max >= 1024 && server.ram_mb_max % 1024 === 0
      ? `${server.ram_mb_max / 1024} GB`
      : `${server.ram_mb_max} MB`;

  // Available mod updates drive the Safe Update action: it only appears when
  // there's something to update, and the count/hover come straight from here.
  // Shares the query key with the Mods tab so both views stay in sync.
  const { data: updates = [] } = useQuery({
    queryKey: ["mod-updates", server.id],
    queryFn: () => api.mods.updates(server.id),
    enabled: canSafeUpdate,
    staleTime: 30_000,
  });

  // Detect an in-flight run (possibly started from the Mods tab) so the button
  // doesn't kick off a second concurrent update.
  const { data: updateRuns = [] } = useQuery({
    queryKey: ["mod-update-runs", server.id],
    queryFn: () => api.mods.updateRuns(server.id, 1),
    enabled: canSafeUpdate,
    // Poll snappily while a run is in flight for smooth progress, and back off
    // when idle to keep the dashboard light.
    refetchInterval: (q) =>
      (q.state.data ?? []).some((r) => r.status === "running") ? 2_000 : 8_000,
  });
  const activeRun = updateRuns.find((r) => r.status === "running") ?? null;

  // When a run settles, refresh the updates list so the button reflects what's
  // actually left to update (e.g. hides once everything succeeded).
  const settledRunRef = useRef<string | null>(null);
  useEffect(() => {
    const latest = updateRuns[0];
    if (!latest || latest.status === "running") return;
    if (settledRunRef.current === latest.id) return;
    settledRunRef.current = latest.id;
    qc.invalidateQueries({ queryKey: ["mod-updates", server.id] });
  }, [updateRuns, qc, server.id]);

  const whitelist = useMutation({
    mutationFn: (name: string) =>
      api.players.action(server.id, { action: "whitelist_add", name }),
    onSuccess: (_d, name) => {
      qc.invalidateQueries({ queryKey: ["players", server.id] });
      success(
        `Whitelisted ${name}`,
        isOnline ? "Command sent to the live server" : "Edited whitelist.json",
      );
      setWhitelistOpen(false);
    },
    onError: (e: Error) => error("Whitelist failed", e.message),
  });

  const backup = useMutation({
    mutationFn: () => api.backups.create(server.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["backups", server.id] });
      success("Backup started", "It will appear under Backups when complete");
    },
    onError: (e: Error) => error("Backup failed to start", e.message),
  });

  const command = useMutation({
    mutationFn: (cmd: string) => api.servers.command(server.id, cmd),
    onSuccess: (_d, cmd) => {
      success("Command sent", `/${cmd}`);
      setCommandOpen(false);
    },
    onError: (e: Error) => error("Command failed", e.message),
  });

  const autoUpdate = useMutation({
    mutationFn: () => api.mods.autoUpdate(server.id),
    onSuccess: (run) => {
      // Seed the run so progress shows immediately, then let polling take over.
      qc.setQueryData<ModUpdateRun[]>(["mod-update-runs", server.id], [run]);
      qc.invalidateQueries({ queryKey: ["mod-update-runs", server.id] });
      success("Safe update started", "Progress is shown here as it runs.");
    },
    onError: (e: Error) => error("Safe update failed to start", e.message),
  });

  // Optionally take a whole-server backup before the run, on top of the engine's
  // own per-mod auto-revert. Mirrors the Mods tab flow.
  const startSafeUpdate = (backupFirst: boolean) => {
    setShowSafeUpdate(false);
    if (backupFirst) {
      api.backups
        .create(server.id)
        .then(() => success("Backup started", "Updating once it's queued"))
        .catch((e: Error) => error("Backup failed to start", e.message));
    }
    autoUpdate.mutate();
  };

  // The auto-update engine only touches enabled Modrinth mods, so the quick
  // action must reflect that exact set — otherwise it offers a "safe update"
  // for disabled/other-source mods it won't actually act on, and the run
  // returns "no updates".
  const safeUpdates = updates.filter(
    (u) => u.enabled && u.source === "modrinth",
  );
  const showSafeUpdateAction = canSafeUpdate && safeUpdates.length > 0;

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatTile
          label="Status"
          value={server.status}
          detail={
            server.status === "online" ? "Accepting commands" : "Not running"
          }
        />
        <StatTile
          label="Address"
          value={`:${server.port}`}
          detail="Server port"
        />
        <StatTile
          label="Memory"
          value={ram}
          detail={`Min ${server.ram_mb_min} MB`}
        />
        <StatTile
          label="Software"
          value={server.platform}
          detail={server.mc_version}
        />
      </div>

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-[1.3fr_1fr]">
        <Panel
          title="Resources"
          description="Live agent metrics for this server."
        >
          <ResourceChart
            serverId={server.id}
            ramMaxMb={server.ram_mb_max}
            status={server.status}
          />
        </Panel>
        <Panel title="Quick Actions">
          <div className="space-y-2">
            {/* While a run is in flight, swap the button for live progress; the
                run may already be going (e.g. started from the Mods tab). */}
            {canSafeUpdate && activeRun ? (
              <SafeUpdateProgress run={activeRun} />
            ) : showSafeUpdateAction ? (
              <div className="group relative">
                <Button
                  variant="outline"
                  className="w-full min-w-0 justify-start"
                  onClick={() => setShowSafeUpdate(true)}
                  loading={autoUpdate.isPending}
                >
                  <ShieldCheck className="h-4 w-4 flex-shrink-0 text-accent" />
                  <span className="truncate">Safe update</span>
                  <Badge variant="success" className="ml-auto flex-shrink-0">
                    {safeUpdates.length}
                  </Badge>
                </Button>
                {/* Hover details: which mods change and from/to versions. */}
                <div className="pointer-events-none absolute left-0 right-0 top-full z-20 mt-1 origin-top rounded-md border border-border bg-surface p-2 opacity-0 shadow-xl transition-opacity duration-100 group-hover:opacity-100">
                  <p className="px-1 pb-1 text-[11px] font-medium uppercase tracking-wide text-text-secondary">
                    {safeUpdates.length} update{safeUpdates.length === 1 ? "" : "s"}{" "}
                    available
                  </p>
                  <div className="max-h-48 space-y-0.5 overflow-y-auto">
                    {safeUpdates.slice(0, 8).map((u) => (
                      <div
                        key={u.mod_id}
                        className="flex items-center gap-2 rounded px-1 py-0.5 text-xs"
                      >
                        <span className="min-w-0 flex-1 truncate text-text-primary">
                          {u.name}
                        </span>
                        <span className="flex items-center gap-1 font-mono text-[11px] text-text-secondary">
                          <span className="truncate">{u.current_version}</span>
                          <ArrowRight className="h-2.5 w-2.5 flex-shrink-0" />
                          <span className="truncate text-green-400">
                            {u.latest_version}
                          </span>
                        </span>
                      </div>
                    ))}
                    {safeUpdates.length > 8 && (
                      <p className="px-1 pt-1 text-[11px] text-text-secondary">
                        +{safeUpdates.length - 8} more…
                      </p>
                    )}
                  </div>
                </div>
              </div>
            ) : null}
            <div className="grid grid-cols-2 gap-2">
              {canConsole && isOnline && (
                <QuickButton
                  icon={<SendHorizontal className="h-4 w-4" />}
                  label="Send command"
                  onClick={() => setCommandOpen(true)}
                />
              )}
              {canWhitelist && (
                <QuickButton
                  icon={<UserPlus className="h-4 w-4" />}
                  label="Add to whitelist"
                  onClick={() => setWhitelistOpen(true)}
                />
              )}
              {canBackup && (
                <QuickButton
                  icon={<Save className="h-4 w-4" />}
                  label={runningBackup ? "Backing up…" : "Backup now"}
                  onClick={() => backup.mutate()}
                  loading={backup.isPending || runningBackup}
                />
              )}
              <QuickButton
                icon={<Terminal className="h-4 w-4" />}
                label="Console"
                onClick={() => onSection("console")}
              />
              <QuickButton
                icon={<FolderTree className="h-4 w-4" />}
                label="Files"
                onClick={() => onSection("files")}
              />
              <QuickButton
                icon={<SlidersHorizontal className="h-4 w-4" />}
                label="Options"
                onClick={() => onSection("options")}
              />
              <QuickButton
                icon={<HardDrive className="h-4 w-4" />}
                label="Backups"
                onClick={() => onSection("backups")}
              />
            </div>
          </div>
        </Panel>
      </div>

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        <Panel
          title="Server Details"
          description="Edit name, directory, and runtime in Options."
          onClick={() => onSection("options")}
        >
          <dl className="grid grid-cols-[100px_minmax(0,1fr)] gap-y-2 text-sm sm:grid-cols-[120px_minmax(0,1fr)]">
            <dt className="text-text-secondary">Name</dt>
            <dd className="truncate text-text-primary">{server.name}</dd>
            <dt className="text-text-secondary">Directory</dt>
            <dd className="truncate font-mono text-text-primary">
              {server.directory_path}
            </dd>
            <dt className="text-text-secondary">Java</dt>
            <dd className="truncate font-mono text-text-primary">
              {server.java_binary}
            </dd>
            <dt className="text-text-secondary">Auto Start</dt>
            <dd className="text-text-primary">
              {server.auto_start ? "Enabled" : "Disabled"}
            </dd>
          </dl>
        </Panel>
        <Panel
          title="Latest Backup"
          description="View and manage all backups."
          onClick={() => onSection("backups")}
        >
          {latestBackup ? (
            <div className="space-y-2 text-sm">
              <div className="flex items-center justify-between">
                <span className="text-text-secondary">Status</span>
                <Badge
                  variant={
                    latestBackup.status === "success"
                      ? "success"
                      : latestBackup.status === "failed"
                        ? "error"
                        : "warning"
                  }
                >
                  {latestBackup.status}
                </Badge>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-text-secondary">Started</span>
                <span>
                  {new Date(latestBackup.started_at).toLocaleString()}
                </span>
              </div>
            </div>
          ) : (
            <p className="text-sm text-text-secondary">No backups yet.</p>
          )}
        </Panel>
      </div>

      <WhitelistQuickAddDialog
        open={whitelistOpen}
        onClose={() => setWhitelistOpen(false)}
        busy={whitelist.isPending}
        serverOnline={isOnline}
        onSubmit={(name) => whitelist.mutate(name)}
      />

      <SendCommandDialog
        open={commandOpen}
        onClose={() => setCommandOpen(false)}
        busy={command.isPending}
        onSubmit={(cmd) => command.mutate(cmd)}
      />

      <SafeUpdateDialog
        open={showSafeUpdate}
        updates={safeUpdates}
        onClose={() => setShowSafeUpdate(false)}
        onConfirm={startSafeUpdate}
      />
    </div>
  );
}
