import { useMemo, useState } from "react";
import { createRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, ChevronDown, ChevronRight, Search } from "lucide-react";
import { Route as rootRoute } from "./__root";
import { Header } from "@/components/layout/header";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { EmptyState } from "@/components/ui/empty-state";
import { ScrollText } from "lucide-react";
import {
  actionCategory,
  actionLabel,
  actionSeverity,
  groupConsecutive,
  type AuditGroup,
  type AuditSeverity,
} from "@/lib/audit";
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

const SEVERITY_DOT: Record<AuditSeverity, string> = {
  high: "bg-red-500",
  notice: "bg-amber-500",
  info: "bg-text-secondary/40",
};

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

type FieldChange = { from: unknown; to: unknown };

// Detect the { changes: { field: { from, to } } } shape produced by config
// edits so we can render a real before/after diff.
function parseChanges(
  detail: Record<string, unknown> | null,
): Record<string, FieldChange> | null {
  const c = detail?.changes;
  if (!c || typeof c !== "object" || Array.isArray(c)) return null;
  const out: Record<string, FieldChange> = {};
  for (const [k, v] of Object.entries(c as Record<string, unknown>)) {
    if (v && typeof v === "object" && "from" in v && "to" in v) {
      out[k] = v as FieldChange;
    }
  }
  return Object.keys(out).length > 0 ? out : null;
}

function fieldLabel(key: string): string {
  return key.replace(/_/g, " ");
}

function fmtVal(v: unknown): string {
  if (v === null || v === undefined || v === "") return "—";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

function ChangeDiff({ changes }: { changes: Record<string, FieldChange> }) {
  return (
    <div className="space-y-1.5 rounded-md border border-border bg-surface-2 p-3 text-xs">
      {Object.entries(changes).map(([field, { from, to }]) => (
        <div key={field} className="flex flex-wrap items-center gap-2">
          <span className="min-w-[7rem] text-text-secondary">{fieldLabel(field)}</span>
          <span className="rounded bg-red-500/10 px-1.5 py-0.5 font-mono text-red-400 line-through decoration-red-400/50">
            {fmtVal(from)}
          </span>
          <ArrowRight className="h-3 w-3 text-text-secondary" />
          <span className="rounded bg-green-500/10 px-1.5 py-0.5 font-mono text-green-400">
            {fmtVal(to)}
          </span>
        </div>
      ))}
    </div>
  );
}

function KeyValues({ detail }: { detail: Record<string, unknown> }) {
  return (
    <dl className="grid grid-cols-[minmax(6rem,auto)_1fr] gap-x-3 gap-y-1 rounded-md border border-border bg-surface-2 p-3 text-xs">
      {Object.entries(detail).map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-text-secondary">{k}</dt>
          <dd className="break-words font-mono text-text-primary">{fmtVal(v)}</dd>
        </div>
      ))}
    </dl>
  );
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
  const severity = actionSeverity(entry.action);
  const detail = parseDetail(entry.detail);
  const changes = parseChanges(detail);
  const expandable = changes !== null || detail !== null;

  return (
    <div className="px-4 py-2.5 sm:px-5">
      <button
        type="button"
        onClick={() => expandable && setOpen((v) => !v)}
        className={`flex w-full items-center gap-3 text-left ${expandable ? "cursor-pointer" : "cursor-default"}`}
      >
        <span className={`h-2 w-2 flex-shrink-0 rounded-full ${SEVERITY_DOT[severity]}`} />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-text-primary">
              {actionLabel(entry.action)}
            </span>
            {categoryBadge(entry.action)}
            {changes && (
              <span className="text-[10px] text-text-secondary">
                {Object.keys(changes).length} change
                {Object.keys(changes).length !== 1 ? "s" : ""}
              </span>
            )}
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
          {changes ? <ChangeDiff changes={changes} /> : detail && <KeyValues detail={detail} />}
          <p className="font-mono text-[11px] text-text-secondary/70">
            {new Date(entry.created_at).toLocaleString()}
          </p>
        </div>
      )}
    </div>
  );
}

// A collapsed run of repeated low-signal events (e.g. sign-ins) by one actor.
function CollapsedRow({
  group,
  actorName,
}: {
  group: AuditGroup;
  actorName: string;
}) {
  const [open, setOpen] = useState(false);
  const count = group.entries.length;
  const newest = group.entries[0];
  const oldest = group.entries[count - 1];

  return (
    <div className="px-4 py-2.5 sm:px-5">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-3 text-left"
      >
        <span className={`h-2 w-2 flex-shrink-0 rounded-full ${SEVERITY_DOT[actionSeverity(group.action)]}`} />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-text-primary">
              {actionLabel(group.action)}
            </span>
            <span className="rounded-full bg-surface-2 px-1.5 py-0.5 text-[10px] font-medium text-text-secondary">
              {count}×
            </span>
            {categoryBadge(group.action)}
          </div>
          <p className="mt-0.5 truncate text-xs text-text-secondary">
            {actorName} · {count} times
          </p>
        </div>
        <span className="flex-shrink-0 text-xs text-text-secondary">
          {relativeTime(newest.created_at)}
        </span>
        {open ? (
          <ChevronDown className="h-4 w-4 flex-shrink-0 text-text-secondary" />
        ) : (
          <ChevronRight className="h-4 w-4 flex-shrink-0 text-text-secondary" />
        )}
      </button>

      {open && (
        <ul className="mt-2 ml-5 space-y-1 border-l border-border pl-3">
          {group.entries.map((e) => (
            <li key={e.id} className="flex items-center justify-between gap-3 text-xs text-text-secondary">
              <span className="font-mono">{new Date(e.created_at).toLocaleString()}</span>
              {e.ip_address && <span className="font-mono">{e.ip_address}</span>}
            </li>
          ))}
        </ul>
      )}
      {!open && oldest.id !== newest.id && (
        <p className="ml-5 mt-1 text-[11px] text-text-secondary/60">
          {relativeTime(oldest.created_at)} – {relativeTime(newest.created_at)}
        </p>
      )}
    </div>
  );
}

// Bucket entries into day sections, newest first, with a friendly label.
function dayLabel(iso: string): string {
  const d = new Date(iso);
  const today = new Date();
  const startOf = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diffDays = Math.round((startOf(today) - startOf(d)) / 86_400_000);
  if (diffDays <= 0) return "Today";
  if (diffDays === 1) return "Yesterday";
  return d.toLocaleDateString(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
    year: d.getFullYear() === today.getFullYear() ? undefined : "numeric",
  });
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
      if (category !== "all" && actionCategory(e.action) !== category) return false;
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

  // Group by day, then collapse repetitive runs within each day.
  const days = useMemo(() => {
    const buckets: { label: string; entries: AuditEntry[] }[] = [];
    for (const e of filtered) {
      const label = dayLabel(e.created_at);
      const last = buckets[buckets.length - 1];
      if (last && last.label === label) last.entries.push(e);
      else buckets.push({ label, entries: [e] });
    }
    return buckets.map((b) => ({
      label: b.label,
      groups: groupConsecutive(b.entries),
    }));
  }, [filtered]);

  return (
    <div>
      <Header
        title="Audit Log"
        description={`${entries.length} recent action${entries.length !== 1 ? "s" : ""}`}
      />
      <div className="space-y-4 p-4 sm:p-6">
        <div className="flex flex-wrap items-center gap-2">
          {/* Search takes the full row on mobile, then shares the row once
              there's room for the filter pills beside it. */}
          <div className="relative w-full min-w-[12rem] sm:w-auto sm:flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-secondary" />
            <Input
              className="pl-9"
              placeholder="Filter by action, user, server…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          {/* Pills wrap individually on tight widths instead of overflowing. */}
          <div className="flex flex-wrap items-center gap-1">
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
          <div className="space-y-4">
            {days.map((day) => (
              <div key={day.label}>
                <div className="mb-1.5 flex items-center gap-2 px-1">
                  <h2 className="text-xs font-semibold uppercase tracking-wide text-text-secondary">
                    {day.label}
                  </h2>
                  <span className="text-[11px] text-text-secondary/60">
                    {day.groups.reduce((n, g) => n + g.entries.length, 0)}
                  </span>
                </div>
                <Card className="divide-y divide-border">
                  {day.groups.map((g) =>
                    g.collapsed ? (
                      <CollapsedRow
                        key={g.key}
                        group={g}
                        actorName={actorName(g.entries[0].user_id)}
                      />
                    ) : (
                      <AuditRow
                        key={g.key}
                        entry={g.entries[0]}
                        actorName={actorName(g.entries[0].user_id)}
                        serverName={serverName(g.entries[0].server_id)}
                      />
                    ),
                  )}
                </Card>
              </div>
            ))}
          </div>
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
