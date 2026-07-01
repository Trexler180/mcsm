// Desktop notifications WITHOUT any third-party push service.
//
// Background Web Push on Chromium browsers (incl. Brave) is routed through
// Google's FCM, gated behind Brave's "Use Google services for push messaging".
// To avoid that dependency entirely, we don't subscribe to push at all. Instead
// we use the local Notification API, driven by the live notification WebSocket
// (lib/notify-stream.ts): whenever an in-app alert arrives and the panel is open
// in a browser tab, we raise a native OS notification.
//
// Trade-off vs. Web Push: notifications only fire while the browser is running
// with the panel open in a tab (foreground or background). There is no
// closed-browser delivery — that's the cost of not using a push service.

const PREF_KEY = "mcsm:desktop-notifications";
// Reuse the dedicated SW (public/push-sw.js) purely for its notificationclick
// handler, so clicking a notification focuses/routes the app. No push here.
const SW_SCOPE = import.meta.env.BASE_URL + "push-sw/";
const SW_URL = import.meta.env.BASE_URL + "push-sw.js";

export function notificationsSupported(): boolean {
  return typeof window !== "undefined" && "Notification" in window;
}

export function notificationPermission(): NotificationPermission {
  return typeof Notification === "undefined" ? "denied" : Notification.permission;
}

function prefEnabled(): boolean {
  try {
    return localStorage.getItem(PREF_KEY) === "1";
  } catch {
    return false;
  }
}

function setPref(on: boolean) {
  try {
    if (on) localStorage.setItem(PREF_KEY, "1");
    else localStorage.removeItem(PREF_KEY);
  } catch {
    /* storage unavailable; treat as off */
  }
}

// desktopNotificationsEnabled is true only when the user opted in on this device
// AND the browser permission is granted — so revoking permission in browser
// settings correctly reads as "off".
export function desktopNotificationsEnabled(): boolean {
  return (
    notificationsSupported() &&
    notificationPermission() === "granted" &&
    prefEnabled()
  );
}

async function swRegistration(): Promise<ServiceWorkerRegistration | undefined> {
  if (!("serviceWorker" in navigator)) return undefined;
  const direct = await navigator.serviceWorker.getRegistration(SW_SCOPE);
  if (direct) return direct;
  const all = await navigator.serviceWorker.getRegistrations();
  const found = all.find((r) => r.scope.endsWith("/push-sw/"));
  if (found) return found;
  try {
    return await navigator.serviceWorker.register(SW_URL, { scope: SW_SCOPE });
  } catch {
    return undefined;
  }
}

export async function enableDesktopNotifications(): Promise<void> {
  if (!notificationsSupported()) {
    throw new Error("This browser doesn't support notifications");
  }
  if (notificationPermission() === "denied") {
    throw new Error(
      "Notifications are blocked for this site. Allow them in your browser's site settings, then try again.",
    );
  }
  const perm = await Notification.requestPermission();
  if (perm !== "granted") {
    throw new Error("Notification permission was not granted");
  }
  // Best-effort: register the SW so clicking a notification routes via its
  // notificationclick handler. Not required for showing notifications.
  await swRegistration();
  setPref(true);
}

export function disableDesktopNotifications(): void {
  // The OS permission can't be revoked programmatically; stop raising them.
  setPref(false);
}

// showDesktopNotification raises a native notification for an alert. Prefers the
// service worker's showNotification (so notificationclick routing works), and
// falls back to the page Notification constructor.
export async function showDesktopNotification(opts: {
  title: string;
  body?: string;
  tag?: string;
  serverId?: string;
}): Promise<void> {
  if (!desktopNotificationsEnabled()) return;
  const data = { serverId: opts.serverId || "" };

  const reg = await swRegistration().catch(() => undefined);
  if (reg && reg.active && "showNotification" in reg) {
    try {
      await reg.showNotification(opts.title, {
        body: opts.body,
        tag: opts.tag,
        data,
      });
      return;
    } catch {
      /* fall through to the page-level Notification */
    }
  }
  try {
    new Notification(opts.title, { body: opts.body, tag: opts.tag });
  } catch {
    /* permission race or unsupported; ignore */
  }
}
