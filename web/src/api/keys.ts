// Every query key in one object, hierarchically arranged so that invalidating a prefix is
// correct by construction rather than by everybody remembering the same string.
export const qk = {
  me: ["me"] as const,
  reminders: (done: boolean) => ["reminders", done] as const,
  rhythm: ["rhythm"] as const,
  devices: ["devices"] as const,
  recovery: ["recovery"] as const,
  relay: ["relay"] as const,
};
