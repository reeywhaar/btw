// Proving who you are: signing in, signing out, and the invitation that starts an account.
//
// Under /api/auth rather than at the top level, because these are one subject and the paths
// should say so. See docs/api_design.md.

import { request } from "@app/api/transport";

export type Me = {
  id: string;
  username: string;
  role: string;
  created_at: number;
};

export type Invite = {
  role: string;
  expires_at: number;
};

export const getAuthMe = () => request<Me>("/api/auth/me");

export const postAuthLogin = (username: string, password: string) =>
  request<void>("/api/auth/login", {
    method: "POST",
    body: { username, password },
  });

export const postAuthLogout = () =>
  request<void>("/api/auth/logout", { method: "POST" });

export const getAuthInvitesByToken = (token: string) =>
  request<Invite>(`/api/auth/invites/${encodeURIComponent(token)}`);

export const postAuthInvitesByTokenAccept = (
  token: string,
  username: string,
  password: string,
) =>
  request<void>(`/api/auth/invites/${encodeURIComponent(token)}/accept`, {
    method: "POST",
    body: { username, password },
  });
