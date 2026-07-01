import { api } from "./api";
import { useNotificationFeed } from "@/store/notification-feed";
import { useNotifications } from "@/store/notifications";
import { showDesktopNotification } from "./desktop-notify";
import type { NotificationItem } from "./types";

type StreamMessage = { type: string; data: NotificationItem };

// NotificationStream maintains a single live WebSocket to /notifications/stream
// and pushes incoming alerts into the feed store and as toasts. Modeled on
// ServerConsole in ws.ts: because the auth ticket is single-use, a fresh one is
// minted on every (re)connect, with exponential backoff.
class NotificationStream {
  private ws: WebSocket | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectDelay = 1000;
  private closed = false;
  private started = false;

  async start() {
    if (this.started) return;
    this.started = true;
    this.closed = false;
    // Seed the feed/unread badge from the server, then go live.
    try {
      const [items, count] = await Promise.all([
        api.notifications.feed({ limit: 50 }),
        api.notifications.unreadCount(),
      ]);
      useNotificationFeed.getState().setInitial(items, count.count);
    } catch {
      // Non-fatal: the stream will still deliver new items.
    }
    this.connect();
  }

  stop() {
    this.closed = true;
    this.started = false;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.ws?.close();
    this.ws = null;
    useNotificationFeed.getState().reset();
  }

  private async connect() {
    if (this.closed) return;
    const ticket = await api.auth
      .ticket()
      .then((t) => t.ticket)
      .catch(() => null);
    if (this.closed || !ticket) {
      this.scheduleReconnect();
      return;
    }
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${protocol}//${window.location.host}/api/v1/notifications/stream?ticket=${encodeURIComponent(ticket)}`;
    this.ws = new WebSocket(url);

    this.ws.onopen = () => {
      this.reconnectDelay = 1000;
      useNotificationFeed.getState().setConnected(true);
    };

    this.ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data) as StreamMessage;
        if (msg.type === "notification" && msg.data) {
          this.onItem(msg.data);
        }
      } catch {
        // ignore malformed frames
      }
    };

    this.ws.onclose = () => {
      useNotificationFeed.getState().setConnected(false);
      this.scheduleReconnect();
    };

    this.ws.onerror = () => this.ws?.close();
  }

  private scheduleReconnect() {
    if (this.closed) return;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.reconnectTimer = setTimeout(() => {
      this.reconnectDelay = Math.min(this.reconnectDelay * 2, 15000);
      this.connect();
    }, this.reconnectDelay);
  }

  private onItem(item: NotificationItem) {
    useNotificationFeed.getState().pushItem(item);
    // Mirror the alert as a transient toast so it's seen even without opening
    // the bell. Critical/warning map to the matching toast variants.
    const notify = useNotifications.getState();
    const variant =
      item.severity === "critical"
        ? "error"
        : item.severity === "warning"
          ? "warning"
          : "default";
    notify.add({ title: item.title, description: item.body, variant });

    // Raise a native OS notification too, when the user enabled them on this
    // device. No push service involved — this fires off the live stream while
    // the panel is open.
    void showDesktopNotification({
      title: item.title,
      body: item.body,
      tag: item.id,
      serverId: item.server_id || undefined,
    });
  }
}

export const notificationStream = new NotificationStream();
