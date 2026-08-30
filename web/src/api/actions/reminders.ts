import { request } from "@app/api/transport";

export type Reminder = {
  id: string;
  text: string;
  /** What the sentence could not hold. Never sent in a push. */
  note: string;
  created_at: number;
  done_at: number | null;
};

// Live and finished are two calls rather than one call with a filter, because they are two
// different screens and the finished list is the one nobody looks at.
export const getReminders = (done = false) =>
  request<{ reminders: Reminder[] }>(
    `/api/reminders${done ? "?done=true" : ""}`,
  );

export const postReminders = (text: string) =>
  request<Reminder>("/api/reminders", { method: "POST", body: { text } });

// Done and Drop end a reminder identically; which was pressed is recorded on the nudge that
// was answered, when there was one. So there is one route here and not two.
/**
 * Changes what a reminder says. Absent leaves a field alone, empty clears it — which is how
 * a description is deleted without also retyping the sentence.
 */
export const patchRemindersById = (
  id: string,
  changes: { text?: string; note?: string },
) =>
  request<Reminder>(`/api/reminders/${id}`, { method: "PATCH", body: changes });

export const postRemindersByIdDone = (id: string) =>
  request<void>(`/api/reminders/${id}/done`, { method: "POST" });

export const postRemindersByIdRevive = (id: string) =>
  request<void>(`/api/reminders/${id}/revive`, { method: "POST" });

export const deleteRemindersById = (id: string) =>
  request<void>(`/api/reminders/${id}`, { method: "DELETE" });
