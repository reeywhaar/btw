// Actions are named mechanically from the route — <method><PathSegmentsInPascalCase>, with
// By<Param> for a path parameter — so the mapping between a call site and a handler is
// reversible and nobody has to guess.

import { request } from "@app/api/transport";

export type Me = {
  id: string;
  username: string;
  role: string;
  created_at: number;
};

export type Reminder = {
  id: string;
  text: string;
  created_at: number;
  done_at: number | null;
};

export type Rhythm = {
  timezone: string;
  window_enabled: boolean;
  wake_minute: number;
  sleep_minute: number;
  budget: number;
  min_gap: number;
  max_budget: number;
};

export type Device = {
  id: string;
  label: string;
  created_at: number;
  last_ok_at: number | null;
  failure_count: number;
  last_error: string;
};

export const getMe = () => request<Me>("/api/me");
export const postLogout = () =>
  request<void>("/api/logout", { method: "POST" });

export const postLogin = (username: string, password: string) =>
  request<void>("/api/login", { method: "POST", body: { username, password } });

export const getInvitesByToken = (token: string) =>
  request<{ role: string; expires_at: number }>(
    `/api/invites/${encodeURIComponent(token)}`,
  );

export const postInvitesByTokenAccept = (
  token: string,
  username: string,
  password: string,
) =>
  request<void>(`/api/invites/${encodeURIComponent(token)}/accept`, {
    method: "POST",
    body: { username, password },
  });

export const getReminders = (done = false) =>
  request<{ reminders: Reminder[] }>(
    `/api/reminders${done ? "?done=true" : ""}`,
  );

export const postReminders = (text: string) =>
  request<Reminder>("/api/reminders", { method: "POST", body: { text } });

export const postRemindersByIdDone = (id: string) =>
  request<void>(`/api/reminders/${id}/done`, { method: "POST" });

export const postRemindersByIdRevive = (id: string) =>
  request<void>(`/api/reminders/${id}/revive`, { method: "POST" });

export const deleteRemindersById = (id: string) =>
  request<void>(`/api/reminders/${id}`, { method: "DELETE" });

export const getRhythm = () => request<Rhythm>("/api/rhythm");

export const patchRhythm = (changes: Partial<Omit<Rhythm, "max_budget">>) =>
  request<Rhythm>("/api/rhythm", { method: "PATCH", body: changes });

export const getPushKey = () => request<{ key: string }>("/api/push/key");

export const getDevices = () => request<{ devices: Device[] }>("/api/devices");

export const postDevices = (device: {
  endpoint: string;
  p256dh: string;
  auth: string;
  label: string;
}) =>
  request<{ id: string; label: string }>("/api/devices", {
    method: "POST",
    body: device,
  });

export const deleteDevicesById = (id: string) =>
  request<void>(`/api/devices/${id}`, { method: "DELETE" });

export const postNudge = () =>
  request<{ sent: boolean }>("/api/nudge", { method: "POST" });
