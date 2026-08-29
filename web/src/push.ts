// Everything about getting a notification onto this particular device.
//
// The capability test is what decides, never a user-agent string: in an iOS Safari tab
// window.PushManager is not restricted, it is *absent*, and it appears once the app is
// launched from the Home Screen. So the same test that rules out a browser too old to help
// also rules out a browser that would work if it were installed, and it will keep being
// right on whatever ships next without anybody editing a regex.

import { getPushKey, postDevices } from "@app/api/actions";

export type PushState =
  | "unsupported" // this browser cannot do it at all
  | "needs-install" // it could, once btw is on the Home Screen
  | "denied" // asked and refused; only the browser's own settings can undo it
  | "ready" // permission granted and a subscription is registered
  | "off"; // supported, not yet asked

export function pushSupported(): boolean {
  return (
    "serviceWorker" in navigator &&
    "PushManager" in window &&
    "Notification" in window
  );
}

/** Whether this document is running as an installed web app rather than in a tab. */
export function installed(): boolean {
  if (window.matchMedia("(display-mode: standalone)").matches) return true;
  // iOS reports its Home Screen apps through a property of its own, which predates the
  // display-mode media query and is still the signal there.
  return (window.navigator as { standalone?: boolean }).standalone === true;
}

/**
 * Whether this is an iOS browser, used *only* to choose which instructions to draw.
 *
 * iPadOS 13 and later report a Mac user agent, so the touch points are what separate an
 * iPad from a laptop.
 */
export function isIOS(): boolean {
  const ua = navigator.userAgent;
  if (/iPhone|iPad|iPod/.test(ua)) return true;
  return /Macintosh/.test(ua) && navigator.maxTouchPoints > 1;
}

export function pushState(): PushState {
  if (!pushSupported()) {
    // On iOS the same absence means two different things, and only one of them is a dead
    // end. Offering an Enable button that cannot work is how somebody taps it, sees
    // nothing, and never comes back.
    return isIOS() && !installed() ? "needs-install" : "unsupported";
  }
  if (Notification.permission === "denied") return "denied";
  if (Notification.permission === "granted") return "ready";
  return "off";
}

/** A name for this device that somebody will recognise in a list. */
function label(): string {
  const ua = navigator.userAgent;
  const browser = /Firefox\//.test(ua)
    ? "Firefox"
    : /Edg\//.test(ua)
      ? "Edge"
      : /Chrome\//.test(ua)
        ? "Chrome"
        : /Safari\//.test(ua)
          ? "Safari"
          : "Browser";
  const platform = /iPhone/.test(ua)
    ? "iPhone"
    : /iPad/.test(ua)
      ? "iPad"
      : /Android/.test(ua)
        ? "Android"
        : /Macintosh/.test(ua)
          ? "Mac"
          : /Windows/.test(ua)
            ? "Windows"
            : "this device";
  return `${browser} on ${platform}`;
}

// Backed by an explicit ArrayBuffer, so the type is Uint8Array<ArrayBuffer> rather than
// the ArrayBufferLike that Uint8Array.from infers — applicationServerKey will not take a
// view that might be over a SharedArrayBuffer.
function base64UrlToBytes(value: string): Uint8Array<ArrayBuffer> {
  const padded =
    value.replace(/-/g, "+").replace(/_/g, "/") +
    "=".repeat((4 - (value.length % 4)) % 4);
  const raw = atob(padded);
  const bytes = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return bytes;
}

function bytesToBase64Url(buffer: ArrayBuffer | null): string {
  if (!buffer) return "";
  const bytes = new Uint8Array(buffer);
  let raw = "";
  for (const b of bytes) raw += String.fromCharCode(b);
  return btoa(raw).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

async function registerWorker(): Promise<ServiceWorkerRegistration> {
  // Scope "/" so the worker covers both shells. sw.js is served from the root for exactly
  // this reason — a worker under /assets could not claim the pages it exists for.
  return navigator.serviceWorker.register("/sw.js", { scope: "/" });
}

/** Hands the current subscription to the server. Idempotent on the endpoint. */
async function send(subscription: PushSubscription): Promise<void> {
  await postDevices({
    endpoint: subscription.endpoint,
    p256dh: bytesToBase64Url(subscription.getKey("p256dh")),
    auth: bytesToBase64Url(subscription.getKey("auth")),
    label: label(),
  });
}

/**
 * Asks for permission and subscribes. Must be called from a user gesture — on iOS
 * strictly, and it is good manners everywhere.
 */
export async function enable(): Promise<PushState> {
  if (!pushSupported()) return pushState();

  const permission = await Notification.requestPermission();
  if (permission !== "granted")
    return permission === "denied" ? "denied" : "off";

  const registration = await registerWorker();
  const { key } = await getPushKey();
  const subscription = await registration.pushManager.subscribe({
    // Required by every browser: a push that shows nothing is a silent channel, and
    // browsers will not hand one out.
    userVisibleOnly: true,
    applicationServerKey: base64UrlToBytes(key),
  });
  await send(subscription);
  return "ready";
}

/**
 * Re-registers whatever subscription this browser currently holds.
 *
 * Called on every load, and this is the mechanism rather than the optimisation. Browsers
 * rotate an endpoint without asking, and support for the pushsubscriptionchange event is
 * uneven — so the reliable way to keep a device alive is to look every time the app is
 * opened, which costs one request and repairs the case that otherwise ends in somebody
 * quietly never being nudged again.
 */
export async function refresh(): Promise<void> {
  if (!pushSupported() || Notification.permission !== "granted") return;
  try {
    const registration = await registerWorker();
    const subscription = await registration.pushManager.getSubscription();
    if (subscription) await send(subscription);
  } catch {
    // Never fatal. A browser that refuses to re-register is a browser that will stop
    // receiving nudges, which the interface already says when the device list is empty.
  }
}
