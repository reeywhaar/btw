import { request } from "@app/api/transport";

export type Reminder = {
  id: string;
  text: string;
  /** What the sentence could not hold. Never sent in a push. */
  note: string;
  /** What the companion called it, or empty until it has been asked. */
  categories: string[] | null;
  created_at: number;
  binned_at: number | null;
};

export const getReminders = () =>
  request<{ reminders: Reminder[] }>("/api/reminders");

/**
 * The bin has a route rather than a parameter, because it is a place rather than a slice of
 * the list — its own screen, its own life, its own sweep.
 */
export const getBin = () => request<{ reminders: Reminder[] }>("/api/bin");

/** Throws away everything in it now, rather than waiting thirty days. */
export const deleteBin = () => request<void>("/api/bin", { method: "DELETE" });

export const postReminders = (text: string) =>
  request<Reminder>("/api/reminders", { method: "POST", body: { text } });

// One gesture, where there were two. What it replaced and why is in docs/conventions.md.
/**
 * Changes what a reminder says. Absent leaves a field alone, empty clears it — which is how
 * a description is deleted without also retyping the sentence.
 */
export const patchRemindersById = (
  id: string,
  changes: { text?: string; note?: string },
) =>
  request<Reminder>(`/api/reminders/${id}`, { method: "PATCH", body: changes });

/** Puts one in the bin, where it stays for thirty days and can be taken back out. */
export const postRemindersByIdBin = (id: string) =>
  request<void>(`/api/reminders/${id}/bin`, { method: "POST" });

export const postRemindersByIdRestore = (id: string) =>
  request<void>(`/api/reminders/${id}/restore`, { method: "POST" });

export const deleteRemindersById = (id: string) =>
  request<void>(`/api/reminders/${id}`, { method: "DELETE" });
