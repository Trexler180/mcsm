import { useMemo, useState } from "react";
import { createRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Search } from "lucide-react";
import { Route as rootRoute } from "./__root";
import { Header } from "@/components/layout/header";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { EmptyState } from "@/components/ui/empty-state";
import { ScrollText } from "lucide-react";
import { actionCategory, actionLabel, actionSeverity } from "@/lib/audit";
import { relativeTime } from "@/lib/time";
import { api } from "@/lib/api";
import type { AuditEntry } from "@/lib/types";

const CATEGORY_STYLES: Record<string, string> = {
  server: "bg-blue-500/15 text-blue-400 border-blue-500/30",
  mod: "bg-purple-500/15 text-purple-400 border-purple-500/30",
  backup: "bg-teal-500/15 text-teal-400 border-teal-500/30",
  auth: "bg-green-500/15 text-green-400 border-green-500/30",
};

const CATEGORY_FILTERS = [
  { value: "all", label: "All" },
  { value: "server", label: "Servers" },
  { value: "mod", label: "Mods" },
  { value: "backup", label: "Backups" },
  { value: "auth", label: "Auth" },
];

function categoryBadge(action: string) {
  const cat = actionCategory(action);
  const cls = CATEGORY_STYLES[cat] ?? "bg-surface-2 text-text-secondary border-border";
  return (
    <span className={`rounded border px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide ${cls}`}>
      {cat}
    </span>
  );
}

// Parse the JSON detail blob into readable key/value pairs. Returns null when
// there's nothing meaningful to show.
function parseDetail(detail: string | null): Record<string, unknown> | null {
  if (!detail || detail === "null") return null;
  try {
    const parsed = JSON.parse(detail);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      const keys = Object.keys(parsed);
      return keys.length > 0 ? (parsed as Record<string, unknown>) : null;
    }
  } catch {
    // Not JSON — fall through.
  }
  return null;
}

function AuditRow({
  entry,
  actorName,
  serverName,
}: {
  entry: AuditEntry;
  actorName: string;
  serverName: string | null;
}) {
  const [open, setOpen] = useState(false);
  const high = actionSeverity(entry.action) === "high";
  const detail = parseDetail(entry.detail);
  const rawDetail = entry.detail && entry.detail !== "null" ? entry.detail : null;
  const expandable = detail !== null || rawDetail !== null;

  return (
    <div className="px-4 py-2.5 sm:px-5">
      <button
        type="button"
        onClick={() => expandable && setOpen((v) => !v)}
        className={`flex w-full items-center gap-3 text-left ${expandable ? "cursor-pointer" : "cursor-default"}`}
      >
        <span
          className={`h-2 w-2 flex-shrink-0 rounded-full ${high ? "bg-red-500" : "bg-text-secondary/40"}`}
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-text-primary">
              {actionLabel(entry.action)}
            </span>
            {categoryBadge(entry.action)}
          </div>
          <p className="mt-0.5 truncate text-xs text-text-secondary">
            {actorName}
            {serverName ? ` · ${serverName}` : ""}
            {entry.ip_address ? ` · ${entry.ip_address}` : ""}
          </p>
        </div>
        <span className="flex-shrink-0 text-xs text-text-secondary">
          {relativeTime(entry.created_at)}
        </span>
        {expandable ? (
          open ? (
            <ChevronDown className="h-4 w-4 flex-shrink-0 text-text-secondary" />
          ) : (
            <ChevronRight className="h-4 w-4 flex-shrink-0 text-text-secondary" />
          )
        ) : (
          <span className="w-4 flex-shrink-0" />
        )}
      </button>

      {open && expandable && (
        <div className="mt-2 ml-5 space-y-2">
          {detail && (
            <dl className="grid grid-cols-[minmax(6rem,auto)_1fr] gap-x-3 gap-y-1 rounded-md border border-border bg-surface-2 p-3 text-xs">
              {Object.entries(detail).map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="text-text-secondary">{k}</dt>
                  <dd className="break-words font-mono text-text-primary">
                    {typeof v === "object" ? JSON.stringify(v) : String(v)}
                  </dd>
                </div>
              ))}
            </dl>
          )}
          <p className="font-mono text-[11px] text-text-secondary/70">
            {new Date(entry.created_at).toLocaleString()}
          </p>
        </div>
      )}
    </div>
  );
}

function AuditPage() {
  const { data: entries = [], isLoading } = useQuery({
    queryKey: ["audit"],
    queryFn: () => api.audit.list(200),
    refetchInterval: 30_000,
  });
  // Resolve ids to names. Both are admin-accessible and cached.
  const { data: servers = [] } = useQuery({
    queryKey: ["servers"],
    queryFn: () => api.servers.list(),
  });
  const { data: users = [] } = useQuery({
    queryKey: ["users"],
    queryFn: () => api.users.list(),
  });

  const serverName = useMemo(() => {
    const m = new Map(servers.map((s) => [s.id, s.name]));
    return (id: string | null) => (id ? (m.get(id) ?? null) : null);
  }, [servers]);
  const actorName = useMemo(() => {
    const m = new Map(
      users.map((u) => [u.id, u.display_name || u.email || u.id.slice(0, 8)]),
    );
    return (id: string | null) => (id ? (m.get(id) ?? "Unknown user") : "System");
  }, [users]);

  const [query, setQuery] = useState("");
  const [category, setCategory] = useState("all");
  const [issuesOnly, setIssuesOnly] = useState(false);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return entries.filter((e) => {
      if (category !== "all" && actionCategory(e.action) !== category)
        return false;
      if (issuesOnly && actionSeverity(e.action) !== "high") return false;
      if (q) {
        const hay = [
          actionLabel(e.action),
          e.action,
          actorName(e.user_id),
          serverName(e.server_id) ?? "",
          e.ip_address ?? "",
        ]
          .join(" ")
          .toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [entries, query, category, issuesOnly, actorName, serverName]);

  return (
    <div>
      <Header
        title="Audit Log"
        description={`${entries.length} recent action${entries.length !== 1 ? "s" : ""}`}
      />
      <div className="space-y-4 p-4 sm:p-6">
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative min-w-[12rem] flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-secondary" />
            <Input
              className="pl-9"
              placeholder="Filter by action, user, server…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          <div className="flex items-center gap-1">
            {CATEGORY_FILTERS.map((c) => (
              <button
                key={c.value}
                onClick={() => setCategory(c.value)}
                className={`rounded-full border px-3 py-1 text-xs transition-colors ${
                  category === c.value
                    ? "border-accent/40 bg-accent/15 text-accent"
                    : "border-border bg-surface-2 text-text-secondary hover:text-text-primary"
                }`}
              >
                {c.label}
              </button>
            ))}
          </div>
          <button
            onClick={() => setIssuesOnly((v) => !v)}
            className={`rounded-full border px-3 py-1 text-xs transition-colors ${
              issuesOnly
                ? "border-red-500/40 bg-red-500/15 text-red-400"
                : "border-border bg-surface-2 text-text-secondary hover:text-text-primary"
            }`}
          >
            Issues only
          </button>
        </div>

        {isLoading ? (
          <div className="flex justify-center py-16">
            <div className="h-6 w-6 animate-spin rounded-full border-2 border-accent border-t-transparent" />
          </div>
        ) : filtered.length === 0 ? (
          <Card>
            <EmptyState
              icon={ScrollText}
              title={entries.length === 0 ? "No audit entries yet" : "No matching entries"}
              hint={
                entries.length === 0
                  ? "Actions across your servers will be recorded here."
                  : "Try a different search or filter."
              }
            />
          </Card>
        ) : (
          <Card className="divide-y divide-border">
            {filtered.map((e) => (
              <AuditRow
                key={e.id}
                entry={e}
                actorName={actorName(e.user_id)}
                serverName={serverName(e.server_id)}
              />
            ))}
          </Card>
        )}
      </div>
    </div>
  );
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/audit",
  component: AuditPage,
});
