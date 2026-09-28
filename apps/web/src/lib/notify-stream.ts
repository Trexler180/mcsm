import { api } from "./api";
import { useNotificationFeed } from "@/store/notification-feed";
import { useNotifications } from "@/store/notifications";
import { showDesktopNotification } from "./desktop-notify";
import {
  asJoinDeniedAlert,
  canWhitelistOn,
  whitelistFromAlert,
} from "./join-denied";
import {
  actionPhrase,
  approveMcpAction,
  asMcpActionAlert,
  denyMcpAction,
  mcpActionToastKey,
  resolvedRequestId,
} from "./mcp-action-alert";
import { useMcpApprovalDialog } from "@/components/layout/mcp-approval-dialog";
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

    // A settled request takes its own prompt down. It may have been answered on
    // another device, or simply lapsed — either way the buttons are dead, and
    // leaving them on screen invites a click that can only fail.
    const resolvedID = resolvedRequestId(item);
    if (resolvedID) {
      notify.removeByKey(mcpActionToastKey(resolvedID));
    }

    const pendingAction = asMcpActionAlert(item);
    const denied = asJoinDeniedAlert(item);
    if (pendingAction) {
      // The agent is blocked on this, so it gets a toast that waits for an
      // answer rather than one that fades — and both answers get a real button,
      // because burying "deny" behind Dismiss would make refusing look like
      // ignoring.
      //
      // No permission pre-check here, unlike the whitelist prompt: this alert is
      // only ever delivered to the one person who can answer it, so there is
      // nobody to show a read-only fallback to.
      const approveLabel = pendingAction.requiresPassword
        ? "Review & approve"
        : "Approve";
      notify.add({
        title: item.title,
        description: item.body,
        variant,
        sticky: true,
        key: mcpActionToastKey(pendingAction.requestId),
        action: {
          label: approveLabel,
          onClick: async () => {
            // With a step-up in force the toast hands off to the dialog, which
            // is the only place a password belongs. Without one, approving is
            // the single click the owner asked for.
            if (pendingAction.requiresPassword) {
              useMcpApprovalDialog.getState().open(pendingAction);
              return;
            }
            try {
              await approveMcpAction(pendingAction.requestId);
              useNotifications
                .getState()
                .success(`Approved: ${actionPhrase(pendingAction.action)}`);
            } catch (e) {
              useNotifications
                .getState()
                .error("Could not approve", (e as Error).message);
            }
          },
        },
        secondary: {
          label: "Deny",
          onClick: async () => {
            try {
              await denyMcpAction(pendingAction.requestId);
              useNotifications.getState().success("Request refused");
            } catch (e) {
              useNotifications
                .getState()
                .error("Could not refuse", (e as Error).message);
            }
          },
        },
      });
      // A prompt nobody answers should not outlive the request behind it.
      const expiresIn =
        new Date(pendingAction.expiresAt).getTime() - Date.now();
      if (Number.isFinite(expiresIn) && expiresIn > 0) {
        setTimeout(
          () =>
            useNotifications
              .getState()
              .removeByKey(mcpActionToastKey(pendingAction.requestId)),
          expiresIn,
        );
      }
    } else if (denied) {
      // This alert is a question, not a status update, so it gets a toast that
      // waits for an answer instead of one that fades. The permission check is
      // awaited before showing anything: a prompt is only useful to someone who
      // can act on it, and the plain toast is the right fallback for everyone
      // else — they should still know somebody tried to get in.
      void canWhitelistOn(denied.serverId).then((allowed) => {
        notify.add({
          title: item.title,
          description: item.body,
          variant,
          sticky: allowed,
          action: allowed
            ? {
                label: `Whitelist ${denied.player}`,
                onClick: async () => {
                  try {
                    await whitelistFromAlert(denied);
                    useNotifications
                      .getState()
                      .success(`${denied.player} is now whitelisted`);
                  } catch (e) {
                    useNotifications
                      .getState()
                      .error(
                        `Could not whitelist ${denied.player}`,
                        (e as Error).message,
                      );
                  }
                },
              }
            : undefined,
        });
      });
    } else {
      notify.add({ title: item.title, description: item.body, variant });
    }

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
