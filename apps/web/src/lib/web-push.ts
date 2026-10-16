import { api } from "./api";
import { notificationPermission, notificationsSupported, swRegistration } from "./desktop-notify";

// Web Push for this browser: alerts that reach the device even when no panel
// tab is open. The panel's own push service worker (public/push-sw.js) shows
// them; this subscribes it with the panel's VAPID key and tells the API where
// to send.

export function pushSupported(): boolean {
  return notificationsSupported() && "serviceWorker" in navigator && "PushManager" in window;
}

function keyBytes(base64url: string): Uint8Array {
  const pad = "=".repeat((4 - (base64url.length % 4)) % 4);
  const raw = atob((base64url + pad).replace(/-/g, "+").replace(/_/g, "/"));
  return Uint8Array.from(raw, (c) => c.charCodeAt(0));
}

/** Waits for a just-registered worker to take over; push needs an active one. */
async function active(reg: ServiceWorkerRegistration): Promise<ServiceWorkerRegistration> {
  if (reg.active) return reg;
  const sw = reg.installing ?? reg.waiting;
  if (!sw) return reg;
  await new Promise<void>((resolve) => {
    sw.addEventListener("statechange", () => {
      if (sw.state === "activated") resolve();
    });
  });
  return reg;
}

/** This browser's push subscription, if it has one. */
export async function pushSubscription(): Promise<PushSubscription | null> {
  if (!pushSupported()) return null;
  const reg = await swRegistration();
  return reg ? reg.pushManager.getSubscription() : null;
}

export async function enablePush(): Promise<void> {
  if (!pushSupported()) throw new Error("This browser can't receive push notifications.");
  if (notificationPermission() === "denied") {
    throw new Error("Notifications are blocked for this site. Allow them in the browser’s site settings, then try again.");
  }
  if ((await Notification.requestPermission()) !== "granted") {
    throw new Error("The browser didn’t allow notifications.");
  }
  const reg = await swRegistration();
  if (!reg) throw new Error("This browser couldn’t set up push.");
  await active(reg);
  const { public_key } = await api.notifications.vapid();
  const sub =
    (await reg.pushManager.getSubscription()) ??
    (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(public_key) as BufferSource }));
  const keys = sub.toJSON().keys ?? {};
  if (!keys.p256dh || !keys.auth) throw new Error("The browser’s push subscription is missing its keys.");
  await api.notifications.registerPush({ endpoint: sub.endpoint, p256dh: keys.p256dh, auth: keys.auth });
}

export async function disablePush(): Promise<void> {
  const sub = await pushSubscription();
  if (!sub) return;
  // Forget it on the panel first: a subscription the panel still holds keeps
  // being sent to even after the browser drops it.
  await api.notifications.unregisterPush(sub.endpoint).catch(() => undefined);
  await sub.unsubscribe();
}
