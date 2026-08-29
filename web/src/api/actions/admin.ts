import { request } from "@app/api/transport";

export type Relay = {
  configured: boolean;
  host: string;
  port: number;
  tls: "starttls" | "implicit";
  username: string;
  // Whether one is stored. The password itself never comes back out — see
  // docs/mail.md — and an empty password on save keeps the stored one.
  password_set: boolean;
  from_address: string;
  sender_name: string;
};

export type RelayEdit = Omit<Relay, "configured" | "password_set"> & {
  password: string;
};

export const getAdminRelay = () => request<Relay>("/api/admin/relay");

export const putAdminRelay = (relay: RelayEdit) =>
  request<Relay>("/api/admin/relay", { method: "PUT", body: relay });

export const deleteAdminRelay = () =>
  request<void>("/api/admin/relay", { method: "DELETE" });

export const postAdminRelayTest = (to: string) =>
  request<{ sent: boolean }>("/api/admin/relay/test", {
    method: "POST",
    body: { to },
  });
