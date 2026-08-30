import { request } from "@app/api/transport";

export type Rhythm = {
  timezone: string;
  window_enabled: boolean;
  wake_minute: number;
  sleep_minute: number;
  budget: number;
  min_gap: number;
  /** Arrive without a sound. The reminder still shows; it does not announce itself. */
  silent: boolean;
  // What the effective window can hold at this spacing, so a control can bound itself
  // rather than offering a number the save will refuse.
  max_budget: number;
};

export const getRhythm = () => request<Rhythm>("/api/rhythm");

export const patchRhythm = (changes: Partial<Omit<Rhythm, "max_budget">>) =>
  request<Rhythm>("/api/rhythm", { method: "PATCH", body: changes });
