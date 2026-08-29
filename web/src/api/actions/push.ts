import { request } from "@app/api/transport";

// Public, and it has to be: the page needs it before there is any question of a session,
// and it is a public key.
export const getPushKey = () => request<{ key: string }>("/api/push/key");
