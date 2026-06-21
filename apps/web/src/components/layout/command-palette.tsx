import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Layers,
  LayoutDashboard,
  ScrollText,
  Search,
  Server as ServerIcon,
  Settings,
  Users,
} from "lucide-react";
import { api } from "@/lib/api";
import { useAuthStore } from "@/store/auth";
import { SERVER_SECTIONS } from "@/components/servers/shared";

type Command = {
  id: string;
  label: string;
  group: string;
  keywords?: string;
  icon: React.ComponentType<{ className?: string }>;
  run: () => void;
};

const PAGES: Array<{
  to: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  adminOnly?: boolean;
}> = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard },
  { to: "/servers", label: "Servers", icon: ServerIcon },
  { to: "/nodes", label: "Nodes", icon: Layers },
  { to: "/users", label: "Users", icon: Users, adminOnly: true },
  { to: "/audit", label: "Audit Log", icon: ScrollText, adminOnly: true },
  { to: "/settings", label: "Settings", icon: Settings, adminOnly: true },
];

/**
 * A Cmd/Ctrl-K command palette for fast navigation: jump to any top-level page,
 * any server, or any section of the server you're currently viewing — without
 * hunting through the sidebar. Power-user complement to the persistent nav.
 */
export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();
  const { user } = useAuthStore();
  const router = useRouterState();
  const pathname = router.location.pathname;

  // Toggle on Cmd/Ctrl-K from anywhere.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // Reset transient state each time the palette opens, and focus the input.
  useEffect(() => {
    if (open) {
      setQuery("");
      setActive(0);
      // Defer so the input exists and isn't stolen by the toggle keystroke.
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  // Only fetch the server list while the palette is open.
  const { data: servers = [] } = useQuery({
    queryKey: ["servers"],
    queryFn: () => api.servers.list(),
    enabled: open,
  });

  const currentServerId = pathname.match(/^\/servers\/([^/]+)/)?.[1];

  const commands = useMemo<Command[]>(() => {
    const close = () => setOpen(false);
    const go = (to: string, params?: Record<string, string>) => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      navigate({ to, params } as any);
      close();
    };

    const cmds: Command[] = [];

    for (const p of PAGES) {
      if (p.adminOnly && user?.role !== "admin") continue;
      cmds.push({
        id: `page:${p.to}`,
        label: p.label,
        group: "Pages",
        icon: p.icon,
        run: () => go(p.to),
      });
    }

    // Sections of the server you're currently inside.
    if (currentServerId) {
      const name = servers.find((s) => s.id === currentServerId)?.name;
      for (const sec of SERVER_SECTIONS) {
        cmds.push({
          id: `section:${sec.value}`,
          label: `Go to ${sec.label}`,
          group: name ? `This server · ${name}` : "This server",
          keywords: sec.value,
          icon: sec.icon,
          run: () =>
            go("/servers/$id/$section", {
              id: currentServerId,
              section: sec.value,
            }),
        });
      }
    }

    // Jump to any server's dashboard.
    for (const s of servers) {
      if (s.id === currentServerId) continue;
      cmds.push({
        id: `server:${s.id}`,
        label: s.name,
        group: "Servers",
        keywords: `${s.status} ${s.platform ?? ""}`,
        icon: ServerIcon,
        run: () =>
          go("/servers/$id/$section", { id: s.id, section: "dashboard" }),
      });
    }

    return cmds;
  }, [servers, currentServerId, user?.role, navigate]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return commands;
    return commands.filter((c) =>
      `${c.label} ${c.group} ${c.keywords ?? ""}`.toLowerCase().includes(q),
    );
  }, [commands, query]);

  // Keep the highlighted index in range as the filtered set shrinks.
  useEffect(() => {
    setActive((a) => Math.min(a, Math.max(0, filtered.length - 1)));
  }, [filtered.length]);

  if (!open) return null;

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      setOpen(false);
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((a) => Math.min(a + 1, filtered.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      filtered[active]?.run();
    }
  };

  return (
    <div
      // Less top offset on small/short screens; the modal is height-capped to
      // the viewport so its header + list always fit (list scrolls internally).
      className="fixed inset-0 z-[100] flex items-start justify-center p-4 pt-[8vh] sm:pt-[12vh]"
      onMouseDown={() => setOpen(false)}
    >
      <div className="absolute inset-0 bg-black/60" />
      <div
        className="relative flex max-h-[80vh] w-full max-w-lg flex-col overflow-hidden rounded-xl border border-border bg-surface shadow-2xl"
        onMouseDown={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
      >
        <div className="flex flex-shrink-0 items-center gap-2 border-b border-border px-3">
          <Search className="h-4 w-4 flex-shrink-0 text-text-secondary" />
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setActive(0);
            }}
            onKeyDown={onKeyDown}
            placeholder="Jump to a page, server, or section…"
            aria-label="Search commands"
            className="h-12 flex-1 bg-transparent text-sm text-text-primary placeholder:text-text-secondary/50 focus:outline-none"
          />
        </div>

        <ul className="min-h-0 flex-1 overflow-y-auto py-1">
          {filtered.length === 0 && (
            <li className="px-4 py-6 text-center text-sm text-text-secondary">
              No matches
            </li>
          )}
          {filtered.map((cmd, i) => {
            const Icon = cmd.icon;
            return (
              <li key={cmd.id}>
                <button
                  type="button"
                  onMouseEnter={() => setActive(i)}
                  onClick={() => cmd.run()}
                  className={`flex w-full items-center gap-3 px-4 py-2.5 text-left text-sm transition-colors ${
                    i === active
                      ? "bg-accent/15 text-text-primary"
                      : "text-text-secondary hover:bg-surface-2"
                  }`}
                >
                  <Icon className="h-4 w-4 flex-shrink-0 text-text-secondary" />
                  <span className="flex-1 truncate">{cmd.label}</span>
                  <span className="flex-shrink-0 text-[10px] uppercase tracking-wide text-text-secondary/60">
                    {cmd.group}
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}
