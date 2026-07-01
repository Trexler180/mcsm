/*
 * Dedicated Web Push service worker.
 *
 * This is intentionally separate from the vite-plugin-pwa (Workbox) service
 * worker and handles ONLY `push` and `notificationclick` — it does no fetch
 * interception or caching. Keeping it isolated means:
 *   1. Push keeps working even though the app builds the PWA SW with
 *      VITE_PWA_SELF_DESTROY=1 (that SW unregisters itself in this deployment).
 *   2. The push surface has no influence over page loads (smaller attack
 *      surface, no cache to poison).
 *
 * It is a static file in public/, so it is NOT processed by Vite — it can't read
 * import.meta.env. The app root is derived from the registration scope instead,
 * which keeps it correct under sub-path hosting (e.g. /dashboard/).
 */

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) =>
  event.waitUntil(self.clients.claim()),
);

// App root is one level up from this SW's scope (".../<base>/push-sw/").
function appRoot() {
  return new URL("../", self.registration.scope);
}

self.addEventListener("push", (event) => {
  let payload = {};
  try {
    payload = event.data ? event.data.json() : {};
  } catch {
    payload = { title: "Notification", body: event.data ? event.data.text() : "" };
  }
  const title = payload.title || "Server Manager";
  const options = {
    body: payload.body || "",
    tag: payload.id || payload.event || undefined,
    data: {
      serverId: payload.server_id || "",
      event: payload.event || "",
    },
    badge: undefined,
    icon: undefined,
  };
  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const serverId = event.notification.data && event.notification.data.serverId;
  const target = new URL(
    serverId ? "servers/" + serverId : "notifications",
    appRoot(),
  ).href;

  event.waitUntil(
    self.clients
      .matchAll({ type: "window", includeUncontrolled: true })
      .then((clients) => {
        // Focus an existing tab if one is already on the app.
        for (const client of clients) {
          if (client.url.startsWith(appRoot().href) && "focus" in client) {
            client.navigate(target).catch(() => {});
            return client.focus();
          }
        }
        return self.clients.openWindow(target);
      }),
  );
});
