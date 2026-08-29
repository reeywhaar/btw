import { request } from "@app/api/transport";

/** What happened to a nudge somebody asked for. */
export type Outcome = "sent" | "nothing" | "undelivered";

/**
 * Send one now — the button that proves the chain in a single press.
 *
 * A reminder's own interval does not apply here. The floor governs when the *scheduler* may
 * raise something; somebody pressing this has asked for a nudge, and "that was raised too
 * recently" refuses a request nobody made on their behalf.
 *
 * None of the three outcomes is an error, which is why they ride in the body rather than in
 * a status code. Two of them are different problems and sending somebody to the wrong one is
 * how a button earns a reputation for lying.
 *
 * Answering a nudge — done and drop — is not here. Those are posted by the service worker
 * from a lock screen, where there is no bundle to import from.
 */
export const postNudges = () =>
  request<{ sent: boolean; outcome: Outcome }>("/api/nudges", {
    method: "POST",
  });
