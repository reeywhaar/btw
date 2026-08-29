import { request } from "@app/api/transport";

export type Device = {
  id: string;
  label: string;
  created_at: number;
  last_ok_at: number | null;
  failure_count: number;
  last_error: string;
};

export type Subscription = {
  endpoint: string;
  p256dh: string;
  auth: string;
  label: string;
  /** Stable per browser. What makes a rotated subscription replace its row rather than
   *  adding one beside it — see push.ts. */
  client_id: string;
};

export const getDevices = () => request<{ devices: Device[] }>("/api/devices");

// Idempotent on the endpoint, and on the browser: re-registering an unchanged subscription
// updates the row it already has, and a *rotated* subscription replaces it rather than
// adding one beside it.
export const postDevices = (device: Subscription) =>
  request<{ id: string; label: string }>("/api/devices", {
    method: "POST",
    body: device,
  });

export const deleteDevicesById = (id: string) =>
  request<void>(`/api/devices/${id}`, { method: "DELETE" });
