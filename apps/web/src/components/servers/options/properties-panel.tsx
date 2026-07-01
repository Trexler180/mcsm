import { useEffect, useRef, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {

  Save,

} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { } from "@/lib/types";
import {
  type PropertiesMap,
  type PropertyField,
  defaultServerProperties,
  parseProperties,
  serializeProperties,
  getPropertyGroups,
} from "./properties-schema";


function PropertyFieldControl({
  field,
  value,
  onChange,
}: {
  field: PropertyField;
  value: string;
  onChange: (value: string) => void;
}) {
  const inputClass =
    "flex h-9 w-full rounded-md border border-border bg-surface-2 px-3 py-1 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent";

  if (field.type === "select") {
    return (
      <select
        className={inputClass}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">Default</option>
        {(field.options ?? []).map((option) => (
          <option key={option} value={option}>
            {option}
          </option>
        ))}
      </select>
    );
  }

  if (field.type === "boolean") {
    const enabled = value === "true";
    return (
      <button
        type="button"
        role="switch"
        aria-checked={enabled}
        onClick={() => onChange(enabled ? "false" : "true")}
        title={enabled ? "Disable" : "Enable"}
        className={`relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors ${
          enabled ? "bg-accent" : "border border-border-hover bg-background"
        }`}
      >
        <span
          className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${
            enabled ? "translate-x-[18px]" : "translate-x-0.5"
          }`}
        />
      </button>
    );
  }

  if (field.type === "textarea") {
    return (
      <textarea
        className="min-h-20 w-full resize-y rounded-md border border-border bg-surface-2 px-3 py-2 text-sm text-text-primary placeholder:text-text-secondary focus:outline-none focus:ring-2 focus:ring-accent"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={field.placeholder}
      />
    );
  }

  return (
    <Input
      type={field.type === "number" ? "number" : "text"}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      min={field.min}
      max={field.max}
      step={field.step}
      placeholder={field.placeholder}
      className={field.type === "list" ? "font-mono" : undefined}
    />
  );
}

export function ServerPropertiesPanel({ serverId }: { serverId: string }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [values, setValues] = useState<PropertiesMap>({});
  const [saveBarPinned, setSaveBarPinned] = useState(false);
  const saveBarSentinelRef = useRef<HTMLDivElement>(null);

  const propsQuery = useQuery({
    queryKey: ["file-content", serverId, "/server.properties"],
    queryFn: () => api.files.readContent(serverId, "/server.properties"),
    retry: false,
  });

  const original = propsQuery.data ?? "";

  // Populate values during render (not in an effect) so the fields' first paint
  // already reflects the loaded data. Doing this in an effect would paint every
  // switch in its default "off" state for one frame, then flip the on-by-default
  // ones, making them animate on load.
  const [loadedFrom, setLoadedFrom] = useState<string | null>(null);
  if (propsQuery.data !== undefined && propsQuery.data !== loadedFrom) {
    setLoadedFrom(propsQuery.data);
    setValues(parseProperties(propsQuery.data));
  }

  useEffect(() => {
    const sentinel = saveBarSentinelRef.current;
    const scrollRoot = sentinel?.closest("[data-server-scroll]");
    if (!sentinel || !(scrollRoot instanceof HTMLElement)) return;

    const observer = new IntersectionObserver(
      ([entry]) => {
        const isPinned = entry.rootBounds
          ? entry.boundingClientRect.top < entry.rootBounds.top
          : !entry.isIntersecting;
        setSaveBarPinned(isPinned);
      },
      {
        root: scrollRoot,
        threshold: 1,
      },
    );

    observer.observe(sentinel);
    return () => observer.disconnect();
  }, []);

  const saveMutation = useMutation({
    mutationFn: () =>
      api.files.writeContent(
        serverId,
        "/server.properties",
        serializeProperties(original, values),
      ),
    onSuccess: () => {
      qc.invalidateQueries({
        queryKey: ["file-content", serverId, "/server.properties"],
      });
      success("server.properties saved");
    },
    onError: (e: Error) => error("Save failed", e.message),
  });

  const createMutation = useMutation({
    mutationFn: () =>
      api.files.writeContent(
        serverId,
        "/server.properties",
        defaultServerProperties,
      ),
    onSuccess: () => {
      qc.invalidateQueries({
        queryKey: ["file-content", serverId, "/server.properties"],
      });
      success("server.properties created");
    },
    onError: (e: Error) => error("Create failed", e.message),
  });

  const setProp = (key: string, value: string) => {
    setValues((prev) => ({ ...prev, [key]: value }));
  };

  return (
    <div>
      {/* Docked toolbar. Once the header below scrolls completely out of view,
          this full-width bar slides DOWN out from behind the page banner with
          a compact copy of the title + Save button. A zero-height sticky host
          keeps it pinned across the whole list without reserving layout; the
          bar is parked above the fold and clipped by the scroll container, so
          its top edge is flush with the banner for the entire slide. */}
      <div className="sticky top-0 z-30 h-0">
        <div
          className={`save-toolbar absolute -left-4 -right-4 top-0 flex items-center justify-between gap-4 border-b border-border bg-surface px-4 py-2.5 sm:-left-6 sm:-right-6 sm:px-6 ${
            saveBarPinned ? "save-toolbar--pinned" : "pointer-events-none"
          }`}
        >
          {/* Title lives in the normal-flow header below; the docked bar only
              re-surfaces the Save action once that header scrolls away, so it
              deliberately omits the title to avoid a duplicate "Minecraft
              Properties" heading. */}
          <span className="truncate text-xs text-text-secondary">
            Unsaved changes save to /server.properties
          </span>
          <Button
            size="sm"
            className="flex-shrink-0"
            onClick={() => saveMutation.mutate()}
            loading={saveMutation.isPending}
            disabled={propsQuery.isLoading || propsQuery.isError}
          >
            <Save className="h-3.5 w-3.5" /> Save Properties
          </Button>
        </div>
      </div>

      {/* Header — normal flow, scrolls away with the page. */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <h3 className="text-sm font-semibold text-text-primary">
            Minecraft Properties
          </h3>
          <p className="text-xs text-text-secondary mt-1">
            Edits /server.properties on the server.
          </p>
        </div>
        <Button
          size="sm"
          onClick={() => saveMutation.mutate()}
          loading={saveMutation.isPending}
          disabled={propsQuery.isLoading || propsQuery.isError}
        >
          <Save className="h-3.5 w-3.5" /> Save Properties
        </Button>
      </div>

      {/* Sentinel at the header's bottom edge: once it leaves the top of the
          scroll area, the header + its Save button are completely out of view
          — exactly the moment the docked toolbar drops down. */}
      <div ref={saveBarSentinelRef} className="h-px" aria-hidden="true" />

      <div className="mt-4">
        {propsQuery.isLoading ? (
        <div className="flex justify-center py-8">
          <div className="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin" />
        </div>
      ) : propsQuery.isError ? (
        <div className="rounded-md border border-border bg-surface p-4 text-sm text-text-secondary">
          <div className="flex items-center justify-between gap-4">
            <span>
              server.properties was not found yet. Start the server once, or
              create a default file.
            </span>
            <Button
              size="sm"
              onClick={() => createMutation.mutate()}
              loading={createMutation.isPending}
            >
              Create
            </Button>
          </div>
        </div>
      ) : (
        <div className="space-y-6">
          {getPropertyGroups(values).map((group) => (
            <section key={group.category} className="space-y-3">
              <h4 className="text-xs font-semibold uppercase tracking-wide text-text-secondary">
                {group.category}
              </h4>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                {group.fields.map((field) => (
                  field.type === "boolean" ? (
                    <div
                      key={field.key}
                      className="flex h-9 items-center justify-between gap-3 rounded-md border border-border bg-surface-2 px-3"
                    >
                      <Label title={field.key} className="min-w-0 truncate">
                        {field.label}
                      </Label>
                      <PropertyFieldControl
                        field={field}
                        value={values[field.key] ?? ""}
                        onChange={(value) => setProp(field.key, value)}
                      />
                    </div>
                  ) : (
                    <div
                      key={field.key}
                      className={
                        field.type === "textarea"
                          ? "space-y-1.5 sm:col-span-2"
                          : "space-y-1.5"
                      }
                    >
                      <Label title={field.key} className="block">
                        {field.label}
                      </Label>
                      <PropertyFieldControl
                        field={field}
                        value={values[field.key] ?? ""}
                        onChange={(value) => setProp(field.key, value)}
                      />
                    </div>
                  )
                ))}
              </div>
            </section>
          ))}
        </div>
        )}
      </div>
    </div>
  );
}

// VersionSelect shows a dropdown of known versions but keeps a "Custom…" escape

export function PropertiesTab({ serverId }: { serverId: string }) {
  return (
    <div className="max-w-5xl pt-4 sm:pt-6">
      <ServerPropertiesPanel serverId={serverId} />
    </div>
  );
}
