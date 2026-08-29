// btw's service worker: turn a push into a notification, and a tap into an answer.
//
// Plain JavaScript, served from the root so its scope covers both shells. It is not part
// of the bundle: a worker with a content hash in its name is a worker the browser cannot
// find at a stable address.

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) =>
  event.waitUntil(self.clients.claim()),
);

self.addEventListener("push", (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    // A push we cannot read is still a push. Showing something is required — the
    // subscription was granted under userVisibleOnly, and a browser that catches us
    // showing nothing may revoke it.
  }

  const body = data.text || "…";
  event.waitUntil(
    self.registration.showNotification("btw", {
      // The sentence somebody wrote is the whole message. A title like "Reminder" above it
      // is a word nobody needs to read twice.
      body,
      // One tag for everything, so a second notification replaces the first on screen
      // rather than stacking. It pairs with the Topic header on the way out, which makes
      // the push service collapse anything it could not deliver.
      tag: "btw",
      renotify: true,
      data: { nudge_id: data.nudge_id },
      actions: [
        { action: "done", title: "Done" },
        { action: "drop", title: "Drop" },
      ],
    }),
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();

  const id = event.notification.data && event.notification.data.nudge_id;
  const action = event.action;

  event.waitUntil(
    (async () => {
      if (id && (action === "done" || action === "drop")) {
        try {
          // Same-origin from a worker, so the session cookie rides along under
          // SameSite=Lax and Sec-Fetch-Site says same-origin — which is why the CSRF
          // guard needs no exception for this.
          const response = await fetch(`/api/nudges/${id}/${action}`, {
            method: "POST",
            credentials: "same-origin",
          });
          if (response.ok) return;
          // A 401 means the session lapsed while the phone was in a pocket. Falling
          // through opens the app, which is the right thing to do about that rather than
          // swallowing it.
        } catch {
          // Offline. Same fallback.
        }
      }
      // Not an action, or the answer did not land: put the app in front of them. Some
      // platforms — iOS among them — may not draw the buttons at all, and this is the
      // path a plain tap takes there.
      const windows = await self.clients.matchAll({
        type: "window",
        includeUncontrolled: true,
      });
      for (const client of windows) {
        if ("focus" in client) return client.focus();
      }
      return self.clients.openWindow("/");
    })(),
  );
});

// Browsers rotate an endpoint without asking. Support for this event is uneven, so it is
// the optimisation and not the mechanism — the app re-registers on every load, which is
// what actually keeps a device alive.
self.addEventListener("pushsubscriptionchange", (event) => {
  event.waitUntil(
    (async () => {
      try {
        const { key } = await (await fetch("/api/push/key")).json();
        const subscription = await self.registration.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: base64UrlToBytes(key),
        });
        await fetch("/api/devices", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            endpoint: subscription.endpoint,
            p256dh: bytesToBase64Url(subscription.getKey("p256dh")),
            auth: bytesToBase64Url(subscription.getKey("auth")),
            label: "",
            // The same stable browser id the app uses, read straight from storage — a
            // worker cannot import from the bundle. Without it this re-subscription would
            // land as a second device and the browser would start showing every nudge
            // twice, which is the exact bug this event is supposed to repair.
            client_id: (await clientID()) || "",
          }),
        });
      } catch {
        // The next time the app is opened repairs it.
      }
    })(),
  );
});

/**
 * The browser id the app keeps in localStorage.
 *
 * A worker has no localStorage of its own, so it asks a window for it. When no window is
 * open there is nobody to ask and the id is empty, which collapses nothing server-side —
 * the safe direction, since a wrong id would delete a device somebody is still using.
 */
async function clientID() {
  const windows = await self.clients.matchAll({
    type: "window",
    includeUncontrolled: true,
  });
  for (const client of windows) {
    try {
      return await new Promise((resolve) => {
        const channel = new MessageChannel();
        channel.port1.onmessage = (e) => resolve(e.data);
        setTimeout(() => resolve(""), 500);
        client.postMessage({ ask: "client-id" }, [channel.port2]);
      });
    } catch {
      // Try the next window.
    }
  }
  return "";
}

function base64UrlToBytes(value) {
  const padded =
    value.replace(/-/g, "+").replace(/_/g, "/") +
    "=".repeat((4 - (value.length % 4)) % 4);
  const raw = atob(padded);
  return Uint8Array.from(raw, (c) => c.charCodeAt(0));
}

function bytesToBase64Url(buffer) {
  if (!buffer) return "";
  let raw = "";
  for (const b of new Uint8Array(buffer)) raw += String.fromCharCode(b);
  return btoa(raw).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
