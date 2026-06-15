import { createRoute, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Plus,
  Play,
  Square,
  RotateCcw,
  Terminal,
  Server,
  Settings,
} from "lucide-react";
import { Route as rootRoute } from "../__root";
import { Header } from "@/components/layout/header";
import { Button } from "@/components/ui/button";
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { useAuthStore } from "@/store/auth";
import { useNotifications } from "@/store/notifications";
import type { Server as ServerType, ServerStatus } from "@/lib/types";

function CreateServerDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const { data: nodes = [] } = useQuery({
    queryKey: ["nodes"],
    queryFn: () => api.nodes.list(),
    enabled: open,
  });

  const [form, setForm] = useState({
    name: "",
    node_id: "",
    directory_path: "",
    platform: "paper",
    mc_version: "1.21.4",
    port: "25565",
    ram_mb_max: "2048",
  });

  const mutation = useMutation({
    mutationFn: () =>
      api.servers.create({
        ...form,
        port: Number(form.port),
        ram_mb_max: Number(form.ram_mb_max),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      success("Server created");
      onClose();
    },
    onError: (e: Error) => error("Failed to create server", e.message),
  });

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }));

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Create Server"
      className="max-w-lg"
    >
      <div className="space-y-4">
        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-1.5 col-span-2">
            <Label>Name</Label>
            <Input
              placeholder="My Server"
              value={form.name}
              onChange={f("name")}
            />
          </div>
          <div className="space-y-1.5 col-span-2">
            <Label>Node</Label>
            <select
              className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
              value={form.node_id}
              onChange={f("node_id")}
            >
              <option value="">Select a node…</option>
              {nodes.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.name} ({n.fqdn})
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1.5 col-span-2">
            <Label>Server Directory</Label>
            <Input
              placeholder="Leave blank for SERVER_ROOT/name"
              value={form.directory_path}
              onChange={f("directory_path")}
            />
          </div>
          <div className="space-y-1.5">
            <Label>Platform</Label>
            <select
              className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
              value={form.platform}
              onChange={f("platform")}
            >
              {[
                "vanilla",
                "paper",
                "purpur",
                "fabric",
                "forge",
                "neoforge",
                "quilt",
                "spigot",
              ].map((p) => (
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
          >
            Create Server
          </Button>
        </div>
      </div>
    </Dialog>
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
    sessionStorage.setItem(`server:${server.id}:tab`, "options");
    navigate({ to: "/servers/$id", params: { id: server.id } });
  };

  return (
    <div className="flex items-center gap-1.5">
      <Button
        size="sm"
        variant="ghost"
        onClick={() =>
          navigate({ to: "/servers/$id", params: { id: server.id } })
        }
        title="Open console"
      >
        <Terminal className="h-3.5 w-3.5" />
      </Button>
      <Button size="sm" variant="ghost" onClick={openSettings} title="Settings">
        <Settings className="h-3.5 w-3.5" />
      </Button>
      {!isOnline ? (
        <Button
          size="sm"
          variant="ghost"
          onClick={() => start.mutate()}
          loading={busy}
          title="Start"
        >
          <Play className="h-3.5 w-3.5 text-green-400" />
        </Button>
      ) : (
        <>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => restart.mutate()}
            loading={busy}
            title="Restart"
          >
            <RotateCcw className="h-3.5 w-3.5 text-yellow-400" />
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => stop.mutate()}
            loading={busy}
            title="Stop"
          >
            <Square className="h-3.5 w-3.5 text-red-400" />
          </Button>
        </>
      )}
    </div>
  );
}

function ServersPage() {
  const [showCreate, setShowCreate] = useState(false);
  const user = useAuthStore((s) => s.user);
  const isAdmin = user?.role === "admin";
  const navigate = useNavigate();
  const { data: servers = [], isLoading } = useQuery({
    queryKey: ["servers"],
    queryFn: () => api.servers.list(),
    refetchInterval: 8_000,
  });

  return (
    <div>
      <Header
        title="Servers"
        description={`${servers.length} server${servers.length !== 1 ? "s" : ""}`}
        actions={
          isAdmin ? (
            <Button onClick={() => setShowCreate(true)} size="sm">
              <Plus className="h-4 w-4" /> New Server
            </Button>
          ) : undefined
        }
      />
      <div className="p-4 sm:p-6">
        {isLoading ? (
          <div className="flex justify-center py-16">
            <div className="w-6 h-6 border-2 border-accent border-t-transparent rounded-full animate-spin" />
          </div>
        ) : servers.length === 0 ? (
          <div className="text-center py-16 text-text-secondary">
            <Server className="h-10 w-10 mx-auto mb-3 opacity-30" />
            <p>No servers yet</p>
            {isAdmin && (
              <Button className="mt-4" onClick={() => setShowCreate(true)}>
                Create your first server
              </Button>
            )}
          </div>
        ) : (
          <>
          {/* Phones: tappable cards instead of a 7-column table. */}
          <div className="space-y-3 md:hidden">
            {servers.map((srv) => (
              <div
                key={srv.id}
                className="rounded-lg border border-border bg-surface p-4 active:bg-surface-2/60"
                onClick={() =>
                  navigate({ to: "/servers/$id", params: { id: srv.id } })
                }
              >
                <div className="flex items-center justify-between gap-3">
                  <span className="min-w-0 truncate text-sm font-medium text-text-primary">
                    {srv.name}
                  </span>
                  <StatusBadge status={srv.status as ServerStatus} />
                </div>
                <p className="mt-1 text-xs text-text-secondary">
                  <span className="capitalize">{srv.platform}</span>{" "}
                  {srv.mc_version} · :{srv.port} · {srv.ram_mb_max} MB
                </p>
                <div
                  className="mt-3 border-t border-border/50 pt-2"
                  onClick={(e) => e.stopPropagation()}
                >
                  <ServerActions server={srv} />
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
              {servers.map((srv) => (
                <TableRow
                  key={srv.id}
                  className="cursor-pointer"
                  onClick={() =>
                    navigate({ to: "/servers/$id", params: { id: srv.id } })
                  }
                >
                  <TableCell className="font-medium">{srv.name}</TableCell>
                  <TableCell>
                    <StatusBadge status={srv.status as ServerStatus} />
                  </TableCell>
                  <TableCell className="capitalize">{srv.platform}</TableCell>
                  <TableCell>{srv.mc_version}</TableCell>
                  <TableCell>{srv.port}</TableCell>
                  <TableCell>{srv.ram_mb_max} MB</TableCell>
                  <TableCell
                    className="text-right"
                    onClick={(e) => e.stopPropagation()}
                  >
                    <ServerActions server={srv} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          </div>
          </>
        )}
      </div>
      {isAdmin && (
        <CreateServerDialog
          open={showCreate}
          onClose={() => setShowCreate(false)}
        />
      )}
    </div>
  );
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/servers",
  component: ServersPage,
});
