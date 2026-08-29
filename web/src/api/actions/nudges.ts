import { request } from "@app/api/transport";

/**
 * Send one now — the button that proves the chain in a single press.
 *
 * `sent: false` is a success. "Everything is finished, or was raised too recently" is a
 * state the interface explains, not a failure of the button.
 *
 * Answering a nudge — done and drop — is not here. Those are posted by the service worker
 * from a lock screen, where there is no bundle to import from.
 */
export const postNudges = () =>
  request<{ sent: boolean }>("/api/nudges", { method: "POST" });
