import { useEffect, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {

  RotateCcw,
  Save,

} from "lucide-react";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { Server } from "@/lib/types";
import { Panel } from "../shared";


function VersionSelect({
  value,
  onChange,
  options,
  loading,
  placeholder,
}: {
  value: string;
  onChange: (v: string) => void;
  options: string[];
  loading?: boolean;
  placeholder?: string;
}) {
  const known = options.includes(value);
  const [custom, setCustom] = useState(!known && value !== "");

  if (custom) {
    return (
      <div className="flex gap-1.5">
        <Input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          className="font-mono"
        />
        <Button
          size="sm"
          variant="ghost"
          onClick={() => {
            setCustom(false);
            onChange(options[0] ?? "");
          }}
          title="Pick from list"
        >
          List
        </Button>
      </div>
    );
  }

  return (
    <select
      className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
      value={value}
      onChange={(e) => {
        if (e.target.value === "__custom__") {
          setCustom(true);
          return;
        }
        onChange(e.target.value);
      }}
    >
      {loading && <option>Loading…</option>}
      {!known && value !== "" && <option value={value}>{value}</option>}
      {options.map((o) => (
        <option key={o} value={o}>
          {o}
        </option>
      ))}
      <option value="__custom__">Custom…</option>
    </select>
  );
}

// Platforms whose install honors a pinned loader version. Others resolve the
// loader themselves, so offering a pin would store metadata that is never used.
const LOADER_PLATFORMS = ["fabric", "quilt"];

export function SoftwareOptionsPanel({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [snapshots, setSnapshots] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [form, setForm] = useState({
    platform: server.platform,
    mc_version: server.mc_version,
    loader_version: server.loader_version ?? "",
    java_binary: server.java_binary,
    jvm_args: server.jvm_args.join(" "),
  });

  useEffect(() => {
    setForm({
      platform: server.platform,
      mc_version: server.mc_version,
      loader_version: server.loader_version ?? "",
      java_binary: server.java_binary,
      jvm_args: server.jvm_args.join(" "),
    });
  }, [server]);

  const versionsQuery = useQuery({
    queryKey: ["mc-versions", form.platform, snapshots],
    queryFn: () => api.minecraft.versions(form.platform, snapshots),
    staleTime: 30 * 60_000,
  });
  const loadersQuery = useQuery({
    queryKey: ["mc-loaders", form.platform],
    queryFn: () => api.minecraft.loaders(form.platform),
    staleTime: 30 * 60_000,
    enabled: LOADER_PLATFORMS.includes(form.platform),
  });
  const mcOptions = (versionsQuery.data ?? []).map((v) => v.version);
  const loaderOptions = (loadersQuery.data ?? []).map((v) => v.version);

  const supportsLoader = LOADER_PLATFORMS.includes(form.platform);
  // A pin only counts on platforms that honor it; a stale loader_version left
  // over from a platform switch still registers as a change so applying clears it.
  const loaderPin = supportsLoader ? form.loader_version || "" : "";
  const versionChanged =
    form.platform !== server.platform ||
    form.mc_version !== server.mc_version ||
    loaderPin !== (server.loader_version ?? "");
  const settingsChanged =
    form.java_binary !== server.java_binary ||
    form.jvm_args !== server.jvm_args.join(" ");

  const saveSettings = () =>
    api.servers.update(server.id, {
      java_binary: form.java_binary,
      jvm_args: form.jvm_args.trim() ? form.jvm_args.trim().split(/\s+/) : [],
    });

  const updateMutation = useMutation({
    mutationFn: saveSettings,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      success("Runtime settings saved");
    },
    onError: (e: Error) => error("Save failed", e.message),
  });

  // Non-version settings may be persisted first, but version metadata is only
  // committed by the backend after the requested runtime installs successfully.
  const reinstallMutation = useMutation({
    mutationFn: async () => {
      if (settingsChanged) await saveSettings();
      await api.servers.reinstall(server.id, {
        platform: form.platform,
        mc_version: form.mc_version,
        loader_version: loaderPin || null,
      });
    },
    onSuccess: () => {
      setConfirmOpen(false);
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      success(
        versionChanged ? "Version applied" : "Runtime repaired",
        "Server stopped and the selected runtime was installed — start it when ready.",
      );
    },
    onError: (e: Error) => {
      setConfirmOpen(false);
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      error("Reinstall failed", e.message);
    },
  });

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }));

  // Reinstalling stops the server, so it needs explicit confirmation. Spell out
  // exactly what will happen for this server's current state.
  const running = ["starting", "online", "stopping"].includes(server.status);
  const imported = Boolean(server.settings?.import);
  const targetLabel = `${form.platform} ${form.mc_version}${loaderPin ? ` (loader ${loaderPin})` : ""}`;
  const confirmDescription = [
    versionChanged
      ? `The ${targetLabel} runtime will be downloaded and installed.`
      : `The ${targetLabel} runtime will be re-downloaded and installed.`,
    running && "The server will be stopped first, disconnecting online players.",
    imported &&
      "This imported server will switch to a panel-managed server.jar; its original launcher will no longer be used.",
    "Worlds, mods, and configuration are not touched.",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <Panel
        title="Runtime"
        description="Choose the configured runtime. Apply changes or repair the managed server.jar without touching worlds, mods, or configuration."
        actions={
          // Wrap + don't shrink so "Apply & reinstall" stays readable next to the
          // long panel description on narrow widths instead of overflowing.
          <div className="flex flex-shrink-0 flex-wrap justify-end gap-2">
            {!versionChanged && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => updateMutation.mutate()}
                loading={updateMutation.isPending}
                disabled={!settingsChanged}
              >
                {!updateMutation.isPending && <Save className="h-3.5 w-3.5" />}
                {settingsChanged ? "Save settings" : "Settings saved"}
              </Button>
            )}
            <Button
              size="sm"
              onClick={() => setConfirmOpen(true)}
              loading={reinstallMutation.isPending}
              disabled={!form.platform || !form.mc_version.trim()}
              className="whitespace-nowrap"
            >
              {!reinstallMutation.isPending && (
                <RotateCcw className="h-3.5 w-3.5" />
              )}
              {versionChanged ? "Apply & reinstall" : "Repair runtime"}
            </Button>
            <ConfirmDialog
              open={confirmOpen}
              onClose={() => setConfirmOpen(false)}
              onConfirm={() => reinstallMutation.mutate()}
              title={versionChanged ? "Apply version change?" : "Repair runtime?"}
              description={confirmDescription}
              confirmLabel="Reinstall now"
              loading={reinstallMutation.isPending}
            />
          </div>
        }
      >
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label>Type</Label>
            <select
              className="flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
              value={form.platform}
              onChange={(e) => {
                const platform = e.target.value;
                // A loader pin belongs to one platform; switching back to the
                // server's own platform restores its pin, anything else clears it.
                setForm((p) => ({
                  ...p,
                  platform,
                  loader_version:
                    platform === server.platform
                      ? (server.loader_version ?? "")
                      : "",
                }));
              }}
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
            <div className="flex items-center justify-between">
              <Label>Minecraft Version</Label>
              <label className="flex items-center gap-1.5 text-xs text-text-secondary cursor-pointer">
                <input
                  type="checkbox"
                  checked={snapshots}
                  onChange={(e) => setSnapshots(e.target.checked)}
                />
                Snapshots
              </label>
            </div>
            <VersionSelect
              value={form.mc_version}
              onChange={(v) => setForm((p) => ({ ...p, mc_version: v }))}
              options={mcOptions}
              loading={versionsQuery.isFetching}
              placeholder="1.21.4"
            />
          </div>
          {supportsLoader && (
            <div className="space-y-1.5">
              <Label>Loader Version</Label>
              {loaderOptions.length > 0 ? (
                <VersionSelect
                  value={form.loader_version}
                  onChange={(v) => setForm((p) => ({ ...p, loader_version: v }))}
                  options={loaderOptions}
                  loading={loadersQuery.isFetching}
                  placeholder="Latest compatible"
                />
              ) : (
                <Input
                  value={form.loader_version}
                  onChange={f("loader_version")}
                  placeholder="Latest compatible"
                />
              )}
            </div>
          )}
          <div className="space-y-1.5">
            <Label>Java Binary</Label>
            <Input
              value={form.java_binary}
              onChange={f("java_binary")}
              className="font-mono"
            />
          </div>
          <div className="col-span-2 space-y-1.5">
            <Label>JVM Arguments</Label>
            <Input
              value={form.jvm_args}
              onChange={f("jvm_args")}
              className="font-mono"
              placeholder="-XX:+UseG1GC"
            />
          </div>
        </div>
    </Panel>
  );
}
