import { createRoute } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Bell,
  BellRing,
  Check,
  CheckCheck,
  Plus,
  Send,
  Trash2,
  Webhook,
  Smartphone,
} from "lucide-react";
import { Route as rootRoute } from "./__root";
import { Header } from "@/components/layout/header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { EmptyState } from "@/components/ui/empty-state";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import { useNotificationFeed } from "@/store/notification-feed";
import {
  notificationsSupported,
  notificationPermission,
  desktopNotificationsEnabled,
  enableDesktopNotifications,
  disableDesktopNotifications,
} from "@/lib/desktop-notify";
import type {
  NotificationChannel,
  NotificationEventDef,
  NotificationItem,
  NotificationSeverity,
  NotificationSubscription,
} from "@/lib/types";

const severityBadge: Record<NotificationSeverity, "muted" | "warning" | "error"> = {
  info: "muted",
  warning: "warning",
  critical: "error",
};

function relativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  const diff = Date.now() - then;
  const s = Math.round(diff / 1000);
  if (s < 60) return "just now";
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.round(h / 24);
  return `${d}d ago`;
}

// ── Inbox ────────────────────────────────────────────────────────

function Inbox() {
  const items = useNotificationFeed((s) => s.items);
  const unread = useNotificationFeed((s) => s.unread);
  const markReadLocal = useNotificationFeed((s) => s.markRead);
  const markAllLocal = useNotificationFeed((s) => s.markAllRead);

  const markAll = useMutation({
    mutationFn: () => api.notifications.markAllRead(),
    onSuccess: () => markAllLocal(),
  });
  const markOne = useMutation({
    mutationFn: (id: string) => api.notifications.markRead(id),
    onSuccess: (_d, id) => markReadLocal(id),
  });

  if (items.length === 0) {
    return (
      <EmptyState
        icon={Bell}
        title="No notifications yet"
        hint="Alerts you subscribe to will appear here in real time."
      />
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-sm text-text-secondary">
          {unread > 0 ? `${unread} unread` : "All caught up"}
        </p>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => markAll.mutate()}
          disabled={unread === 0 || markAll.isPending}
        >
          <CheckCheck className="h-4 w-4" /> Mark all read
        </Button>
      </div>
      <div className="space-y-2">
        {items.map((item) => (
          <FeedRow key={item.id} item={item} onRead={() => markOne.mutate(item.id)} />
        ))}
      </div>
    </div>
  );
}

function FeedRow({ item, onRead }: { item: NotificationItem; onRead: () => void }) {
  const unread = !item.read_at;
  return (
    <div
      className={
        "flex items-start gap-3 rounded-lg border border-border p-4 " +
        (unread ? "bg-surface" : "bg-surface/40")
      }
    >
      <div className="mt-0.5">
        <Badge variant={severityBadge[item.severity]}>{item.severity}</Badge>
      </div>
      <div className="min-w-0 flex-1">
        <p className="font-medium text-text-primary">{item.title}</p>
        {item.body && (
          <p className="mt-0.5 text-sm text-text-secondary">{item.body}</p>
        )}
        <p className="mt-1 text-xs text-text-secondary">
          {relativeTime(item.created_at)}
        </p>
      </div>
      {unread && (
        <button
          onClick={onRead}
          title="Mark read"
          className="rounded-md p-1.5 text-text-secondary hover:bg-surface-2 hover:text-text-primary"
        >
          <Check className="h-4 w-4" />
        </button>
      )}
    </div>
  );
}

// ── Subscriptions ────────────────────────────────────────────────

function DesktopToggle() {
  const { success, error } = useNotifications();
  const [enabled, setEnabled] = useState(false);
  const [blocked, setBlocked] = useState(false);
  const [busy, setBusy] = useState(false);
  const supported = notificationsSupported();

  // Reconcile from ground truth (opt-in flag + browser permission) so the toggle
  // reflects reality after enabling, reloading, or revoking permission.
  const refresh = () => {
    setBlocked(notificationPermission() === "denied");
    setEnabled(desktopNotificationsEnabled());
  };

  useEffect(() => {
    refresh();
  }, []);

  const toggle = async () => {
    setBusy(true);
    try {
      if (enabled) {
        disableDesktopNotifications();
        success("Desktop notifications disabled on this device");
      } else {
        await enableDesktopNotifications();
        success("Desktop notifications enabled on this device");
      }
    } catch (e) {
      error("Could not enable notifications", (e as Error).message);
    } finally {
      refresh();
      setBusy(false);
    }
  };

  const description = !supported
    ? "This browser does not support notifications."
    : blocked
      ? "Notifications are blocked for this site — allow them in your browser's site settings, then click Enable."
      : enabled
        ? "Enabled. You'll get native desktop notifications for in-app alerts while the panel is open in a browser tab."
        : "Show native desktop notifications for in-app alerts while the panel is open. No third-party push service is used.";

  return (
    <div className="flex items-start gap-3 rounded-lg border border-border bg-surface p-4">
      <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-accent/10">
        <Smartphone className="h-4 w-4 text-accent" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <p className="font-medium text-text-primary">Desktop notifications on this device</p>
          {enabled && <Badge variant="success">Enabled</Badge>}
          {blocked && <Badge variant="warning">Blocked</Badge>}
        </div>
        <p className="mt-0.5 text-sm text-text-secondary">{description}</p>
      </div>
      <Button
        size="sm"
        variant={enabled ? "outline" : "default"}
        onClick={toggle}
        loading={busy}
        disabled={!supported}
      >
        {enabled ? "Disable" : "Enable"}
      </Button>
    </div>
  );
}

function SubscriptionsTab() {
  const qc = useQueryClient();
  const { error } = useNotifications();

  const events = useQuery({
    queryKey: ["notif-events"],
    queryFn: () => api.notifications.events(),
  });
  const subs = useQuery({
    queryKey: ["notif-subs"],
    queryFn: () => api.notifications.subscriptions.list(),
  });
  const channels = useQuery({
    queryKey: ["notif-channels"],
    queryFn: () => api.notifications.channels.list(),
  });

  const upsert = useMutation({
    mutationFn: (data: {
      event_type: string;
      min_severity: NotificationSeverity;
      channels: string[];
      enabled: boolean;
    }) => api.notifications.subscriptions.upsert(data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["notif-subs"] }),
    onError: (e: Error) => error("Could not save subscription", e.message),
  });

  const subByType = useMemo(() => {
    const map = new Map<string, NotificationSubscription>();
    // Only the all-servers rules (server_id null) drive this matrix.
    (subs.data ?? [])
      .filter((s) => s.server_id === null)
      .forEach((s) => map.set(s.event_type, s));
    return map;
  }, [subs.data]);

  if (events.isLoading) return <Spinner />;

  return (
    <div className="space-y-4">
      <DesktopToggle />
      <div className="rounded-lg border border-border">
        {(events.data ?? []).map((def, i) => (
          <SubscriptionRow
            key={def.type}
            def={def}
            sub={subByType.get(def.type)}
            channels={channels.data ?? []}
            first={i === 0}
            onChange={(payload) => upsert.mutate(payload)}
          />
        ))}
      </div>
    </div>
  );
}

function SubscriptionRow({
  def,
  sub,
  channels,
  first,
  onChange,
}: {
  def: NotificationEventDef;
  sub?: NotificationSubscription;
  channels: NotificationChannel[];
  first: boolean;
  onChange: (data: {
    event_type: string;
    min_severity: NotificationSeverity;
    channels: string[];
    enabled: boolean;
  }) => void;
}) {
  const enabled = sub?.enabled ?? false;
  const minSeverity = sub?.min_severity ?? def.default_severity;
  const selected = new Set(sub?.channels ?? ["inapp"]);

  const emit = (next: {
    enabled?: boolean;
    min_severity?: NotificationSeverity;
    channels?: string[];
  }) =>
    onChange({
      event_type: def.type,
      enabled: next.enabled ?? enabled,
      min_severity: next.min_severity ?? minSeverity,
      channels: next.channels ?? Array.from(selected),
    });

  const toggleChannel = (sel: string) => {
    const set = new Set(selected);
    if (set.has(sel)) set.delete(sel);
    else set.add(sel);
    emit({ channels: Array.from(set) });
  };

  return (
    <div
      className={
        "flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between " +
        (first ? "" : "border-t border-border")
      }
    >
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <button
            type="button"
            role="switch"
            aria-checked={enabled}
            aria-label={enabled ? "Disable" : "Enable"}
            onClick={() => emit({ enabled: !enabled })}
            className={
              "relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors " +
              (enabled ? "bg-accent" : "bg-surface-2 border border-border")
            }
          >
            <span
              className={
                "inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform " +
                (enabled ? "translate-x-[18px]" : "translate-x-0.5")
              }
            />
          </button>
          <p className="font-medium text-text-primary">{def.label}</p>
        </div>
        <p className="mt-1 pl-11 text-sm text-text-secondary">{def.description}</p>
      </div>

      <div
        className={
          "flex flex-wrap items-center gap-3 pl-11 sm:pl-0 " +
          (enabled ? "" : "pointer-events-none opacity-40")
        }
      >
        <select
          value={minSeverity}
          onChange={(e) =>
            emit({ min_severity: e.target.value as NotificationSeverity })
          }
          className="h-8 rounded-md border border-border bg-surface px-2 text-xs text-text-primary"
        >
          <option value="info">Info+</option>
          <option value="warning">Warning+</option>
          <option value="critical">Critical only</option>
        </select>

        <ChannelChip
          label="In-app"
          active={selected.has("inapp")}
          onClick={() => toggleChannel("inapp")}
        />
        <ChannelChip
          label="Push"
          active={selected.has("webpush")}
          onClick={() => toggleChannel("webpush")}
        />
        {channels.map((ch) => (
          <ChannelChip
            key={ch.id}
            label={ch.label || ch.config.format}
            active={selected.has(`webhook:${ch.id}`)}
            onClick={() => toggleChannel(`webhook:${ch.id}`)}
          />
        ))}
      </div>
    </div>
  );
}

function ChannelChip({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={
        "rounded-full border px-2.5 py-1 text-xs transition-colors " +
        (active
          ? "border-accent bg-accent/15 text-accent"
          : "border-border text-text-secondary hover:text-text-primary")
      }
    >
      {label}
    </button>
  );
}

// ── Channels (webhooks) ──────────────────────────────────────────

function ChannelsTab() {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const channels = useQuery({
    queryKey: ["notif-channels"],
    queryFn: () => api.notifications.channels.list(),
  });

  const [label, setLabel] = useState("");
  const [url, setUrl] = useState("");
  const [format, setFormat] = useState("generic");
  const [secret, setSecret] = useState("");

  const create = useMutation({
    mutationFn: () =>
      api.notifications.channels.create({
        label: label.trim(),
        url: url.trim(),
        format,
        secret: secret.trim() || undefined,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["notif-channels"] });
      setLabel("");
      setUrl("");
      setSecret("");
      setFormat("generic");
      success("Webhook added");
    },
    onError: (e: Error) => error("Could not add webhook", e.message),
  });

  return (
    <div className="space-y-5">
      <div className="rounded-lg border border-border bg-surface p-5">
        <div className="mb-3 flex items-center gap-2">
          <Webhook className="h-4 w-4 text-accent" />
          <h3 className="font-semibold text-text-primary">Add a webhook</h3>
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <div>
            <Label htmlFor="wh-label">Label</Label>
            <Input
              id="wh-label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="My Discord channel"
            />
          </div>
          <div>
            <Label htmlFor="wh-format">Format</Label>
            <select
              id="wh-format"
              value={format}
              onChange={(e) => setFormat(e.target.value)}
              className="h-9 w-full rounded-md border border-border bg-surface px-3 text-sm text-text-primary"
            >
              <option value="generic">Generic JSON (HMAC signed)</option>
              <option value="discord">Discord</option>
              <option value="slack">Slack</option>
            </select>
          </div>
          <div className="sm:col-span-2">
            <Label htmlFor="wh-url">Webhook URL</Label>
            <Input
              id="wh-url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://discord.com/api/webhooks/…"
            />
          </div>
          {format === "generic" && (
            <div className="sm:col-span-2">
              <Label htmlFor="wh-secret">Signing secret (optional)</Label>
              <Input
                id="wh-secret"
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
                placeholder="Used to HMAC-sign the X-MCSM-Signature header"
              />
            </div>
          )}
        </div>
        <div className="mt-4">
          <Button
            onClick={() => create.mutate()}
            loading={create.isPending}
            disabled={!url.trim()}
          >
            <Plus className="h-4 w-4" /> Add webhook
          </Button>
        </div>
      </div>

      {channels.isLoading ? (
        <Spinner />
      ) : (channels.data ?? []).length === 0 ? (
        <EmptyState
          icon={Webhook}
          title="No webhooks yet"
          hint="Add a Discord, Slack, or generic webhook to forward alerts."
        />
      ) : (
        <div className="space-y-2">
          {(channels.data ?? []).map((ch) => (
            <ChannelRow key={ch.id} channel={ch} />
          ))}
        </div>
      )}
    </div>
  );
}

function ChannelRow({ channel }: { channel: NotificationChannel }) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();

  const test = useMutation({
    mutationFn: () => api.notifications.channels.test(channel.id),
    onSuccess: () => success("Test sent", "Check your webhook destination"),
    onError: (e: Error) => error("Test failed", e.message),
  });
  const remove = useMutation({
    mutationFn: () => api.notifications.channels.remove(channel.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["notif-channels"] });
      success("Webhook removed");
    },
    onError: (e: Error) => error("Could not remove", e.message),
  });

  return (
    <div className="flex items-center gap-3 rounded-lg border border-border bg-surface p-4">
      <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-surface-2">
        <Webhook className="h-4 w-4 text-text-secondary" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <p className="font-medium text-text-primary">
            {channel.label || "Webhook"}
          </p>
          <Badge variant="muted">{channel.config.format}</Badge>
          {channel.config.secret_set && <Badge variant="muted">signed</Badge>}
        </div>
        <p className="mt-0.5 truncate text-xs text-text-secondary">
          {channel.config.url}
        </p>
      </div>
      <Button size="sm" variant="ghost" onClick={() => test.mutate()} loading={test.isPending}>
        <Send className="h-4 w-4" /> Test
      </Button>
      <Button
        size="sm"
        variant="ghost"
        onClick={() => remove.mutate()}
        loading={remove.isPending}
        className="text-red-400 hover:text-red-300"
      >
        <Trash2 className="h-4 w-4" />
      </Button>
    </div>
  );
}

// ── Page ─────────────────────────────────────────────────────────

function Spinner() {
  return (
    <div className="flex justify-center py-16">
      <div className="h-6 w-6 animate-spin rounded-full border-2 border-accent border-t-transparent" />
    </div>
  );
}

function NotificationsPage() {
  const [tab, setTab] = useState("inbox");
  const unread = useNotificationFeed((s) => s.unread);

  return (
    <div>
      <Header
        title="Notifications"
        description="Subscribe to alerts and choose how you're notified"
      />
      <div className="max-w-3xl space-y-5 p-4 sm:p-6">
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList>
            <TabsTrigger value="inbox">
              <span className="inline-flex items-center gap-1.5">
                {unread > 0 ? (
                  <BellRing className="h-4 w-4" />
                ) : (
                  <Bell className="h-4 w-4" />
                )}
                Inbox{unread > 0 ? ` (${unread})` : ""}
              </span>
            </TabsTrigger>
            <TabsTrigger value="subscriptions">Subscriptions</TabsTrigger>
            <TabsTrigger value="channels">Webhooks</TabsTrigger>
          </TabsList>
        </Tabs>

        {tab === "inbox" && <Inbox />}
        {tab === "subscriptions" && <SubscriptionsTab />}
        {tab === "channels" && <ChannelsTab />}
      </div>
    </div>
  );
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/notifications",
  component: NotificationsPage,
});
