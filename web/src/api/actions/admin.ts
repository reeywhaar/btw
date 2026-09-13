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

/** How this instance reaches the internet, when it cannot reach it directly. */
export type Proxy = {
  configured: boolean;
  kind: "" | "proxio" | "socks5";
  /** The address with no path. How a request is built out of it is the kind's business. */
  url: string;
  /** Empty for kinds that authenticate with a secret alone, which is proxio. */
  username: string;
  /**
   * Whether one is stored. The credential itself never comes back out — see docs/proxies.md —
   * and an empty token on save keeps the stored one, so long as the endpoint is unchanged.
   */
  token_set: boolean;
  /** Off keeps the address and the credential, so switching back on is a press. */
  enabled: boolean;
};

export type ProxyEdit = {
  kind: "proxio" | "socks5";
  url: string;
  username: string;
  token: string;
};

export type ProxyTest = {
  /** Every service it got to. They are separate hosts and are blocked separately. */
  reached: { label: string; url: string }[];
  took_ms: number;
};

export const getAdminProxy = () => request<Proxy>("/api/admin/proxy");

export const putAdminProxy = (proxy: ProxyEdit) =>
  request<Proxy>("/api/admin/proxy", { method: "PUT", body: proxy });

/** Switches an existing proxy on or off without touching what it holds. */
export const patchAdminProxy = (enabled: boolean) =>
  request<Proxy>("/api/admin/proxy", { method: "PATCH", body: { enabled } });

export const deleteAdminProxy = () =>
  request<void>("/api/admin/proxy", { method: "DELETE" });

export const postAdminProxyTest = () =>
  request<ProxyTest>("/api/admin/proxy/test", { method: "POST", body: {} });

/** The model every account that never chose one follows. */
/** One service's default model. A slug belongs to a service, so there is one of these each. */
export type ServiceDefault = {
  provider: string;
  label: string;
  model: string;
  /** What a blank falls through to, so the form need not name a model of its own. */
  fallback_model: string;
};

export type DefaultModel = {
  providers: ServiceDefault[];
  model_limit: number;
};

export const getAdminDefaultModel = () =>
  request<DefaultModel>("/api/admin/companion");

export const putAdminDefaultModel = (provider: string, model: string) =>
  request<DefaultModel>("/api/admin/companion", {
    method: "PUT",
    body: { provider, model },
  });
