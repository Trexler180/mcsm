import { createRoute, useNavigate } from "@tanstack/react-router";
import { useCallback, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Plus,
  Play,
  Square,
  RotateCcw,
  Terminal,
  Server,
  Settings,
  ChevronDown,
  Folder,
  FolderInput,
} from "lucide-react";
import { clsx } from "clsx";
import { Route as rootRoute } from "../__root";
import { Header } from "@/components/layout/header";
import { durationSince } from "@/lib/time";
import { Button } from "@/components/ui/button";
import { PermissionButton } from "@/components/ui/permission";
import {
  ServerPermissionsProvider,
  listPermissions,
} from "@/lib/server-permissions";
import { StatusBadge } from "@/components/ui/badge";
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from "@/components/ui/table";
import { Dialog } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { groupServersByFolder, folderAccent } from "@/lib/folders";
import {
  FolderSelect,
  ManageFoldersDialog,
  MoveToFolderDialog,
  useServerFolders,
} from "@/components/servers/folder-dialogs";
import { useAuthStore } from "@/store/auth";
import { useNotifications } from "@/store/notifications";
import type {
  Server as ServerType,
  ServerFolder,
  ServerStatus,
} from "@/lib/types";

const PLATFORMS = [
  "vanilla",
  "paper",
  "purpur",
  "fabric",
  "forge",
  "neoforge",
  "quilt",
  "spigot",
];

function CreateServerDialog({
  open,
  onClose,
  folders,
  defaultFolderId,
}: {
  open: boolean;
  onClose: () => void;
  folders: ServerFolder[];
  /** Pre-selected when the dialog is opened from a folder's own header. */
  defaultFolderId: string | null;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [folderId, setFolderId] = useState<string | null>(defaultFolderId);
  const { data: nodes = [] } = useQuery({
    queryKey: ["nodes"],
    queryFn: () => api.nodes.list(),
    enabled: open,
  });

  // "new" provisions a fresh runtime; "import" adopts an existing directory and
  // runs it as-is without touching its files.
  const [mode, setMode] = useState<"new" | "import">("new");
  const [form, setForm] = useState({
    name: "",
    node_id: "",
    directory_path: "",
    platform: "paper",
    mc_version: "1.21.4",
    port: "25565",
    ram_mb_max: "2048",
  });
  const [selectedDir, setSelectedDir] = useState("");
  const [jarFile, setJarFile] = useState("");

  const { data: candidates = [], isFetching: scanning } = useQuery({
    queryKey: ["import-candidates", form.node_id],
    queryFn: () => api.servers.importCandidates(form.node_id),
    enabled: open && mode === "import" && !!form.node_id,
  });
  const selected = candidates.find((c) => c.directory === selectedDir);

  const mutation = useMutation({
    mutationFn: () =>
      api.servers.create({
        ...form,
        port: Number(form.port),
        ram_mb_max: Number(form.ram_mb_max),
        folder_id: folderId,
        ...(mode === "import"
          ? {
              import_existing: true,
              jar_file: jarFile,
              directory_path: selectedDir,
            }
          : {}),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      // The new server changes its folder's count.
      qc.invalidateQueries({ queryKey: ["server-folders"] });
      success(mode === "import" ? "Server imported" : "Server created");
      onClose();
    },
    onError: (e: Error) =>
      error(
        mode === "import" ? "Failed to import server" : "Failed to create server",
        e.message,
      ),
  });

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }));

  // Picking a directory pre-fills the form from what the agent detected; every
  // field stays editable so the user can correct a wrong guess.
  const pickDirectory = (dir: string) => {
    setSelectedDir(dir);
    const c = candidates.find((x) => x.directory === dir);
    if (!c) {
      setJarFile("");
      return;
    }
    setJarFile(c.jar_file);
    setForm((p) => ({
      ...p,
      name: p.name || c.directory,
      directory_path: c.directory,
      platform: c.platform || p.platform,
      mc_version: c.mc_version || p.mc_version,
      port: c.port ? String(c.port) : p.port,
    }));
  };

  const switchMode = (next: "new" | "import") => {
    setMode(next);
    setSelectedDir("");
    setJarFile("");
  };

  const canSubmit =
    !!form.name.trim() &&
    !!form.node_id &&
    (mode === "new" || !!selectedDir);

  const tabClass = (active: boolean) =>
    `flex-1 h-9 rounded-md text-sm font-medium transition-colors ${
      active
        ? "bg-accent text-black"
        : "text-text-secondary hover:text-text-primary"
    }`;

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Add Server"
      className="max-w-lg"
    >
      <div className="space-y-4">
        <div className="flex gap-1 rounded-lg border border-border bg-surface-2 p-1">
          <button className={tabClass(mode === "new")} onClick={() => switchMode("new")}>
            New server
          </button>
          <button
            className={tabClass(mode === "import")}
            onClick={() => switchMode("import")}
          >
            Import existing
          </button>
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-1.5 col-span-2">
            <Label>Node</Label>
            <select
              className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
              value={form.node_id}
              onChange={(e) => {
                setForm((p) => ({ ...p, node_id: e.target.value }));
                setSelectedDir("");
              }}
            >
              <option value="">Select a node…</option>
              {nodes.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.name} ({n.fqdn})
                </option>
              ))}
            </select>
          </div>

          {mode === "import" && (
            <div className="space-y-1.5 col-span-2">
              <Label>Existing server directory</Label>
              {!form.node_id ? (
                <p className="text-sm text-text-secondary">
                  Select a node first to scan for servers.
                </p>
              ) : scanning ? (
                <p className="text-sm text-text-secondary">Scanning…</p>
              ) : candidates.length === 0 ? (
                <p className="text-sm text-text-secondary">
                  No unmanaged server directories found in SERVER_ROOT on this
                  node.
                </p>
              ) : (
                <select
                  className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
                  value={selectedDir}
                  onChange={(e) => pickDirectory(e.target.value)}
                >
                  <option value="">Select a directory…</option>
                  {candidates.map((c) => (
                    <option key={c.directory} value={c.directory}>
                      {c.directory}
                      {c.platform ? ` — ${c.platform}` : ""}
                      {c.mc_version ? ` ${c.mc_version}` : ""}
                    </option>
                  ))}
                </select>
              )}
              {selected && (
                <div className="flex flex-wrap gap-1.5 pt-1 text-xs">
                  <Chip>{selected.jar_file || "no jar found"}</Chip>
                  {selected.has_world && <Chip>world present</Chip>}
                  {selected.mod_count > 0 && <Chip>{selected.mod_count} mods</Chip>}
                  {selected.plugin_count > 0 && (
                    <Chip>{selected.plugin_count} plugins</Chip>
                  )}
                  <Chip>
                    EULA {selected.eula_accepted ? "accepted" : "not accepted"}
                  </Chip>
                </div>
              )}
              <p className="text-xs text-text-secondary pt-1">
                Files are left untouched — the server runs exactly what's on disk.
              </p>
            </div>
          )}

          <div className="space-y-1.5 col-span-2">
            <Label>Name</Label>
            <Input
              placeholder="My Server"
              value={form.name}
              onChange={f("name")}
            />
          </div>

          {/* Only worth the row once folders exist. */}
          {folders.length > 0 && (
            <div className="space-y-1.5 col-span-2">
              <Label>Folder</Label>
              <FolderSelect
                value={folderId}
                onChange={setFolderId}
                folders={folders}
              />
            </div>
          )}

          {mode === "new" && (
            <div className="space-y-1.5 col-span-2">
              <Label>Server Directory</Label>
              <Input
                placeholder="Leave blank for SERVER_ROOT/name"
                value={form.directory_path}
                onChange={f("directory_path")}
              />
            </div>
          )}

          <div className="space-y-1.5">
            <Label>Platform</Label>
            <select
              className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
              value={form.platform}
              onChange={f("platform")}
            >
              {PLATFORMS.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1.5">
            <Label>MC Version</Label>
            <Input
              placeholder="1.21.4"
              value={form.mc_version}
              onChange={f("mc_version")}
            />
          </div>
          <div className="space-y-1.5">
            <Label>Port</Label>
            <Input type="number" value={form.port} onChange={f("port")} />
          </div>
          <div className="space-y-1.5">
            <Label>Max RAM (MB)</Label>
            <Input
              type="number"
              value={form.ram_mb_max}
              onChange={f("ram_mb_max")}
            />
          </div>
        </div>
        <div className="flex justify-end gap-3 pt-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            onClick={() => mutation.mutate()}
            loading={mutation.isPending}
            disabled={!canSubmit}
          >
            {mode === "import" ? "Import Server" : "Create Server"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

function Chip({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded border border-border bg-surface-2 px-1.5 py-0.5 text-text-secondary">
      {children}
    </span>
  );
}

function ServerActions({ server }: { server: ServerType }) {
  const qc = useQueryClient();
  const { error } = useNotifications();
  const navigate = useNavigate();

  const start = useMutation({
    mutationFn: () => api.servers.start(server.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["servers"] }),
    onError: (e: Error) => error("Start failed", e.message),
  });
  const stop = useMutation({
    mutationFn: () => api.servers.stop(server.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["servers"] }),
    onError: (e: Error) => error("Stop failed", e.message),
  });
  const restart = useMutation({
    mutationFn: () => api.servers.restart(server.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["servers"] }),
    onError: (e: Error) => error("Restart failed", e.message),
  });

  const isOnline = server.status === "online" || server.status === "starting";
  const busy = start.isPending || stop.isPending || restart.isPending;

  const openSettings = () => {
    navigate({
      to: "/servers/$id/$section",
      params: { id: server.id, section: "options" },
    });
  };

  // The list endpoint resolves each row's permissions for the caller, so the
  // row's actions can be gated without a request per server.
  return (
    <ServerPermissionsProvider permissions={listPermissions(server.permissions)}>
      <div className="flex items-center gap-1.5">
        <PermissionButton
          need="console"
          size="sm"
          variant="ghost"
          onClick={() =>
            navigate({
              to: "/servers/$id/$section",
              params: { id: server.id, section: "console" },
            })
          }
          title="Open console"
          aria-label="Open console"
        >
          <Terminal className="h-3.5 w-3.5" />
        </PermissionButton>
        <PermissionButton
          need="settings"
          size="sm"
          variant="ghost"
          onClick={openSettings}
          title="Settings"
        >
          <Settings className="h-3.5 w-3.5" />
        </PermissionButton>
        {!isOnline ? (
          <PermissionButton
            need="power.start"
            size="sm"
            variant="ghost"
            onClick={() => start.mutate()}
            loading={busy}
            title="Start"
          >
            <Play className="h-3.5 w-3.5 text-green-400" />
          </PermissionButton>
        ) : (
          <>
            <PermissionButton
              need="power.restart"
              size="sm"
              variant="ghost"
              onClick={() => restart.mutate()}
              loading={busy}
              title="Restart"
            >
              <RotateCcw className="h-3.5 w-3.5 text-yellow-400" />
            </PermissionButton>
            <PermissionButton
              need="power.stop"
              size="sm"
              variant="ghost"
              onClick={() => stop.mutate()}
              loading={busy}
              title="Stop"
            >
              <Square className="h-3.5 w-3.5 text-red-400" />
            </PermissionButton>
          </>
        )}
      </div>
    </ServerPermissionsProvider>
  );
}

// "up 3h 12m" next to an online badge, from the uptime tracker's online_since.
// Renders nothing for offline servers or before the first tracked start.
function UptimeHint({ server }: { server: ServerType }) {
  if (server.status !== "online") return null;
  const up = durationSince(server.online_since);
  if (!up) return null;
  return (
    <span
      className="whitespace-nowrap text-[11px] text-text-secondary"
      title={
        server.online_since
          ? `Online since ${new Date(server.online_since * 1000).toLocaleString()}`
          : undefined
      }
    >
      up {up}
    </span>
  );
}

// Mirrors the real layout: stacked cards on phones, a table on md+. Same shape
// as the loaded content so nothing shifts when the data lands.
function ServersSkeleton() {
  return (
    <>
      <div className="space-y-3 md:hidden">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="rounded-lg border border-border bg-surface p-4">
            <div className="flex items-center justify-between gap-3">
              <Skeleton className="h-4 w-32" />
              <Skeleton className="h-5 w-16 rounded-full" />
            </div>
            <Skeleton className="mt-2 h-3 w-48" />
            <div className="mt-3 border-t border-border/50 pt-2">
              <Skeleton className="h-7 w-40" />
            </div>
          </div>
        ))}
      </div>

      <div className="hidden md:block">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Platform</TableHead>
              <TableHead>Version</TableHead>
              <TableHead>Port</TableHead>
              <TableHead>RAM</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {Array.from({ length: 5 }).map((_, i) => (
              <TableRow key={i}>
                <TableCell>
                  <Skeleton className="h-4 w-28" />
                </TableCell>
                <TableCell>
                  <Skeleton className="h-5 w-16 rounded-full" />
                </TableCell>
                <TableCell>
                  <Skeleton className="h-4 w-16" />
                </TableCell>
                <TableCell>
                  <Skeleton className="h-4 w-12" />
                </TableCell>
                <TableCell>
                  <Skeleton className="h-4 w-12" />
                </TableCell>
                <TableCell>
                  <Skeleton className="h-4 w-16" />
                </TableCell>
                <TableCell className="text-right">
                  <Skeleton className="ml-auto h-7 w-32" />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </>
  );
}

// Phones: tappable cards instead of a 7-column table.
function ServerCards({
  servers,
  canMove,
  onMove,
}: {
  servers: ServerType[];
  canMove: boolean;
  onMove: (s: ServerType) => void;
}) {
  const navigate = useNavigate();
  return (
    <div className="space-y-3 md:hidden">
      {servers.map((srv) => (
        <div
          key={srv.id}
          className="rounded-lg border border-border bg-surface p-4 active:bg-surface-2/60"
          onClick={() =>
            navigate({
              to: "/servers/$id/$section",
              params: { id: srv.id, section: "dashboard" },
            })
          }
        >
          <div className="flex items-center justify-between gap-3">
            <span className="min-w-0 truncate text-sm font-medium text-text-primary">
              {srv.name}
            </span>
            <span className="flex shrink-0 items-center gap-2">
              <UptimeHint server={srv} />
              <StatusBadge status={srv.status as ServerStatus} />
            </span>
          </div>
          <p className="mt-1 text-xs text-text-secondary">
            <span className="capitalize">{srv.platform}</span> {srv.mc_version} ·
            :{srv.port} · {srv.ram_mb_max} MB
          </p>
          <div
            className="mt-3 flex items-center justify-between gap-2 border-t border-border/50 pt-2"
            onClick={(e) => e.stopPropagation()}
          >
            <ServerActions server={srv} />
            {canMove && (
              <Button
                size="sm"
                variant="ghost"
                onClick={() => onMove(srv)}
                title="Move to folder"
                aria-label={`Move ${srv.name} to a folder`}
              >
                <FolderInput className="h-3.5 w-3.5" />
              </Button>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

function ServerTable({
  servers,
  canMove,
  onMove,
}: {
  servers: ServerType[];
  canMove: boolean;
  onMove: (s: ServerType) => void;
}) {
  const navigate = useNavigate();
  return (
    <div className="hidden md:block">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Platform</TableHead>
            <TableHead>Version</TableHead>
            <TableHead>Port</TableHead>
            <TableHead>RAM</TableHead>
            <TableHead className="text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {servers.map((srv) => (
            <TableRow
              key={srv.id}
              className="cursor-pointer"
              onClick={() =>
                navigate({
                  to: "/servers/$id/$section",
                  params: { id: srv.id, section: "dashboard" },
                })
              }
            >
              <TableCell className="font-medium">{srv.name}</TableCell>
              <TableCell>
                <span className="flex items-center gap-2">
                  <StatusBadge status={srv.status as ServerStatus} />
                  <UptimeHint server={srv} />
                </span>
              </TableCell>
              <TableCell className="capitalize">{srv.platform}</TableCell>
              <TableCell>{srv.mc_version}</TableCell>
              <TableCell>{srv.port}</TableCell>
              <TableCell>{srv.ram_mb_max} MB</TableCell>
              <TableCell
                className="text-right"
                onClick={(e) => e.stopPropagation()}
              >
                <div className="flex items-center justify-end gap-1.5">
                  <ServerActions server={srv} />
                  {canMove && (
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => onMove(srv)}
                      title="Move to folder"
                      aria-label={`Move ${srv.name} to a folder`}
                    >
                      <FolderInput className="h-3.5 w-3.5" />
                    </Button>
                  )}
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

// Collapsed sections survive reloads — an operator who keeps "Minigames" shut
// should not have to re-collapse it on every visit.
const COLLAPSE_KEY = "mcsm:collapsed-folders";

function readCollapsed(): Set<string> {
  try {
    const raw = localStorage.getItem(COLLAPSE_KEY);
    return new Set(raw ? (JSON.parse(raw) as string[]) : []);
  } catch {
    return new Set();
  }
}

/**
 * One folder's section: a header strip carrying the folder's accent, its
 * description and count, and the servers inside. A null folder renders the
 * trailing "Ungrouped" bucket.
 */
function FolderSection({
  folder,
  servers,
  collapsed,
  onToggle,
  isAdmin,
  onMove,
  onAddServer,
}: {
  folder: ServerFolder | null;
  servers: ServerType[];
  collapsed: boolean;
  onToggle: () => void;
  isAdmin: boolean;
  onMove: (s: ServerType) => void;
  onAddServer: (folderId: string | null) => void;
}) {
  const accent = folderAccent(folder?.color);
  const label = folder?.name ?? "Ungrouped";

  return (
    <section className="space-y-3">
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={!collapsed}
          className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-1 py-1 text-left hover:bg-surface-2/60"
        >
          <ChevronDown
            className={clsx(
              "h-4 w-4 flex-shrink-0 text-text-secondary transition-transform",
              collapsed && "-rotate-90",
            )}
          />
          {folder ? (
            <span
              className={clsx(
                "h-2.5 w-2.5 flex-shrink-0 rounded-full",
                accent.dot,
              )}
            />
          ) : (
            <Folder className="h-3.5 w-3.5 flex-shrink-0 text-text-secondary" />
          )}
          <span className="truncate text-sm font-semibold text-text-primary">
            {label}
          </span>
          <span className="flex-shrink-0 rounded-full border border-border bg-surface-2 px-1.5 text-xs tabular-nums text-text-secondary">
            {servers.length}
          </span>
          {folder?.description && (
            <span className="hidden min-w-0 truncate text-xs text-text-secondary sm:inline">
              {folder.description}
            </span>
          )}
        </button>
        {isAdmin && (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => onAddServer(folder?.id ?? null)}
            title={folder ? `New server in ${folder.name}` : "New server"}
            aria-label={folder ? `New server in ${folder.name}` : "New server"}
            className="flex-shrink-0"
          >
            <Plus className="h-3.5 w-3.5" />
          </Button>
        )}
      </div>

      {!collapsed &&
        (servers.length === 0 ? (
          <p className="px-2 pb-2 text-xs text-text-secondary">
            Empty — use the move button on a server to file it here.
          </p>
        ) : (
          <>
            <ServerCards servers={servers} canMove={isAdmin} onMove={onMove} />
            <ServerTable servers={servers} canMove={isAdmin} onMove={onMove} />
          </>
        ))}
    </section>
  );
}

function ServersPage() {
  const [showCreate, setShowCreate] = useState(false);
  const [createFolderId, setCreateFolderId] = useState<string | null>(null);
  const [showFolders, setShowFolders] = useState(false);
  const [moving, setMoving] = useState<ServerType | null>(null);
  const [collapsed, setCollapsed] = useState<Set<string>>(readCollapsed);

  const user = useAuthStore((s) => s.user);
  const isAdmin = user?.role === "admin";

  const { data: servers = [], isLoading } = useQuery({
    queryKey: ["servers"],
    queryFn: () => api.servers.list(),
    refetchInterval: 8_000,
  });
  const { data: folders = [] } = useServerFolders();

  const toggleFolder = useCallback((key: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      try {
        localStorage.setItem(COLLAPSE_KEY, JSON.stringify([...next]));
      } catch {
        // A full or blocked storage quota must not break collapsing.
      }
      return next;
    });
  }, []);

  const openCreate = (folderId: string | null) => {
    setCreateFolderId(folderId);
    setShowCreate(true);
  };

  const groups = groupServersByFolder(servers, folders);
  // Sections are noise until folders exist; without any, keep the flat list.
  const grouped = folders.length > 0;

  return (
    <div>
      <Header
        title="Servers"
        description={`${servers.length} server${servers.length !== 1 ? "s" : ""}${
          folders.length > 0
            ? ` in ${folders.length} folder${folders.length !== 1 ? "s" : ""}`
            : ""
        }`}
        actions={
          isAdmin ? (
            <div className="flex items-center gap-2">
              <Button
                onClick={() => setShowFolders(true)}
                size="sm"
                variant="outline"
              >
                <Folder className="h-4 w-4" /> Folders
              </Button>
              <Button onClick={() => openCreate(null)} size="sm">
                <Plus className="h-4 w-4" /> New Server
              </Button>
            </div>
          ) : undefined
        }
      />
      <div className="p-4 sm:p-6">
        {isLoading ? (
          <ServersSkeleton />
        ) : servers.length === 0 && folders.length === 0 ? (
          <div className="text-center py-16 text-text-secondary">
            <Server className="h-10 w-10 mx-auto mb-3 opacity-30" />
            <p>No servers yet</p>
            {isAdmin && (
              <Button className="mt-4" onClick={() => openCreate(null)}>
                Create your first server
              </Button>
            )}
          </div>
        ) : grouped ? (
          <div className="space-y-6">
            {groups.map(({ folder, servers: inFolder }) => {
              const key = folder?.id ?? "__ungrouped";
              return (
                <FolderSection
                  key={key}
                  folder={folder}
                  servers={inFolder}
                  collapsed={collapsed.has(key)}
                  onToggle={() => toggleFolder(key)}
                  isAdmin={isAdmin}
                  onMove={setMoving}
                  onAddServer={openCreate}
                />
              );
            })}
          </div>
        ) : (
          <>
            <ServerCards servers={servers} canMove={false} onMove={setMoving} />
            <ServerTable servers={servers} canMove={false} onMove={setMoving} />
          </>
        )}
      </div>

      {isAdmin && (
        <>
          {/* Remount on folder change so the dialog picks up the new default. */}
          <CreateServerDialog
            key={createFolderId ?? "root"}
            open={showCreate}
            onClose={() => setShowCreate(false)}
            folders={folders}
            defaultFolderId={createFolderId}
          />
          <ManageFoldersDialog
            open={showFolders}
            onClose={() => setShowFolders(false)}
          />
          <MoveToFolderDialog
            server={moving}
            folders={folders}
            onClose={() => setMoving(null)}
          />
        </>
      )}
    </div>
  );
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/servers",
  component: ServersPage,
});

