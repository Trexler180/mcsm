import { useEffect, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {

  RotateCcw,
  Save,

} from "lucide-react";
import { Button } from "@/components/ui/button";
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

export function SoftwareOptionsPanel({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [snapshots, setSnapshots] = useState(false);
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
  });
  const mcOptions = (versionsQuery.data ?? []).map((v) => v.version);
  const loaderOptions = (loadersQuery.data ?? []).map((v) => v.version);

  const save = () =>
    api.servers.update(server.id, {
      platform: form.platform,
      mc_version: form.mc_version,
      loader_version: form.loader_version || null,
      java_binary: form.java_binary,
      jvm_args: form.jvm_args.trim() ? form.jvm_args.trim().split(/\s+/) : [],
    });

  const updateMutation = useMutation({
    mutationFn: save,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      success("Runtime settings saved");
    },
    onError: (e: Error) => error("Save failed", e.message),
  });

  // Save the new version then force the agent to re-fetch the runtime jar, so the
  // change actually applies (a normal save only takes effect if no jar exists).
  const reinstallMutation = useMutation({
    mutationFn: async () => {
      await save();
      await api.servers.reinstall(server.id);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      success(
        "Version applied",
        "Server stopped and runtime reinstalled — start it to run the new version.",
      );
    },
    onError: (e: Error) => error("Reinstall failed", e.message),
  });

  const f =
    (k: keyof typeof form) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((p) => ({ ...p, [k]: e.target.value }));

  // Switching the Minecraft/loader version needs a runtime reinstall to take
  // effect; other edits (java binary, JVM args) only need a save.
  const versionChanged =
    form.platform !== server.platform ||
    form.mc_version !== server.mc_version ||
    (form.loader_version || "") !== (server.loader_version ?? "");
  const dirty =
    versionChanged ||
    form.java_binary !== server.java_binary ||
    form.jvm_args !== server.jvm_args.join(" ");

  return (
    <Panel
        title="Runtime"
        description="Choose the server runtime and version. Changing the Minecraft or loader version reinstalls the server runtime."
        actions={
          // Wrap + don't shrink so "Apply & reinstall" stays readable next to the
          // long panel description on narrow widths instead of overflowing.
          <div className="flex flex-shrink-0 flex-wrap justify-end gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => updateMutation.mutate()}
              loading={updateMutation.isPending}
              disabled={!dirty}
            >
              {!updateMutation.isPending && <Save className="h-3.5 w-3.5" />}
              {dirty ? "Save" : "Saved"}
            </Button>
            {versionChanged && (
              <Button
                size="sm"
                onClick={() => reinstallMutation.mutate()}
                loading={reinstallMutation.isPending}
                className="whitespace-nowrap"
              >
                {!reinstallMutation.isPending && (
                  <RotateCcw className="h-3.5 w-3.5" />
                )}
                Apply &amp; reinstall
              </Button>
            )}
          </div>
        }
      >
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label>Type</Label>
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
