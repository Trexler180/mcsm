import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, ExternalLink, Globe2, Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import type { Server } from "@/lib/types";

// Mirrors the API's ValidatePublicSlug: DNS-label shape, since the slug
// doubles as a subdomain. The server re-validates (and owns the reserved-word
// list); this is just fast feedback.
const SLUG_RE = /^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$/;

function suggestSlug(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63);
}

export function PublicStatusPanel({ server }: { server: Server }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  // Older development API binaries do not include these additive fields.
  // Normalize at the boundary so the rest of the panel always works with the
  // string/boolean values its controls expect.
  const savedEnabled = server.public_status ?? false;
  const savedSlug = server.public_slug ?? "";

  const [enabled, setEnabled] = useState(savedEnabled);
  const [slug, setSlug] = useState(savedSlug);
  const [copied, setCopied] = useState(false);

  const updateMutation = useMutation({
    mutationFn: () =>
      api.servers.update(server.id, {
        public_status: enabled,
        public_slug: slug.trim().toLowerCase(),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server", server.id] });
      qc.invalidateQueries({ queryKey: ["servers"] });
      success(
        enabled ? "Public status page enabled" : "Public status page disabled",
      );
    },
    onError: (e: Error) => error("Save failed", e.message),
  });

  const normalized = slug.trim().toLowerCase();
  const slugValid = normalized.length >= 3 && SLUG_RE.test(normalized);
  const dirty = enabled !== savedEnabled || normalized !== savedSlug;
  const canSave = dirty && (!enabled || slugValid);

  // The API serves the page at the panel origin's /status/<slug>; when a
  // dedicated status domain is configured it is additionally reachable at
  // https://<slug>.<status domain>/.
  const pageURL = `${window.location.origin}/status/${normalized}`;
  const liveURL = `${window.location.origin}/status/${savedSlug}`;
  const isLive = savedEnabled && savedSlug !== "";

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(isLive ? liveURL : pageURL);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      error("Copy failed", "Clipboard is not available");
    }
  };

  return (
    <section className="overflow-hidden rounded-lg border border-border bg-surface">
      <div className="flex items-center gap-2.5 border-b border-border bg-surface-2/40 px-3.5 py-2.5">
        <div className="grid h-7 w-7 flex-shrink-0 place-items-center rounded-md border border-accent/20 bg-accent/10 text-accent">
          <Globe2 className="h-4 w-4" />
        </div>
        <h2 className="text-sm font-semibold text-text-primary">
          Public Status Page
        </h2>
        <span className="truncate text-xs text-text-secondary">
          — anyone with the link can see it
        </span>
      </div>

      <div className="space-y-3 p-3.5">
        <label className="flex cursor-pointer items-start gap-2.5">
          <input
            type="checkbox"
            className="mt-0.5"
            checked={enabled}
            onChange={(e) => {
              setEnabled(e.target.checked);
              if (e.target.checked && !slug) setSlug(suggestSlug(server.name));
            }}
          />
          <span>
            <span className="block text-sm text-text-primary">
              Enable the public status page
            </span>
            <span className="block text-xs text-text-secondary">
              Shows only: server name, online/offline, player count, Minecraft
              version, and uptime history. No addresses, ports, or player
              names. Requires no login.
            </span>
          </span>
        </label>

        {enabled && (
          <div className="space-y-1">
            <Label>Public URL name</Label>
            <Input
              value={slug}
              onChange={(e) => setSlug(e.target.value)}
              className="font-mono"
              placeholder="my-server"
            />
            {!slugValid && normalized !== "" && (
              <p className="text-xs text-red-400">
                3–63 lowercase letters, digits, and inner hyphens — it becomes
                part of a URL and subdomain.
              </p>
            )}
            {slugValid && (
              <p className="break-all text-xs text-text-secondary">
                Page will be at <span className="font-mono">{pageURL}</span>
              </p>
            )}
          </div>
        )}

        {isLive && (
          <div className="flex flex-wrap items-center gap-2 rounded-md border border-border bg-surface-2/40 px-3 py-2">
            <span className="min-w-0 flex-1 break-all font-mono text-xs text-text-primary">
              {liveURL}
            </span>
            <Button variant="outline" size="sm" onClick={copy}>
              {copied ? (
                <Check className="h-3.5 w-3.5 text-green-400" />
              ) : (
                <Copy className="h-3.5 w-3.5" />
              )}
              {copied ? "Copied" : "Copy"}
            </Button>
            <a href={liveURL} target="_blank" rel="noreferrer">
              <Button variant="outline" size="sm">
                <ExternalLink className="h-3.5 w-3.5" /> Open
              </Button>
            </a>
          </div>
        )}
      </div>

      <div className="flex items-center justify-between gap-3 border-t border-border bg-surface-2/30 px-3.5 py-2">
        <span className="text-xs text-text-secondary">
          {dirty ? "Unsaved changes" : "All changes saved"}
        </span>
        <Button
          size="sm"
          onClick={() => updateMutation.mutate()}
          loading={updateMutation.isPending}
          disabled={!canSave}
        >
          {!updateMutation.isPending && <Save className="h-3.5 w-3.5" />}
          {dirty ? "Save" : "Saved"}
        </Button>
      </div>
    </section>
  );
}
