import { request } from "@app/api/transport";

export type Rhythm = {
  timezone: string;
  window_enabled: boolean;
  wake_minute: number;
  sleep_minute: number;
  budget: number;
  /** Arrive without a sound. The reminder still shows; it does not announce itself. */
  silent: boolean;
  /** The most anybody may ask for. A plain number — nothing about the window bounds it. */
  max_budget: number;
};

export const getRhythm = () => request<Rhythm>("/api/rhythm");

export const patchRhythm = (changes: Partial<Omit<Rhythm, "max_budget">>) =>
  request<Rhythm>("/api/rhythm", { method: "PATCH", body: changes });
