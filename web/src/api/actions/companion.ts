import { request } from "@app/api/transport";

export type Companion = {
  configured: boolean;
  model: string;
  /**
   * Whether a key is stored. The key itself never comes back out — see docs/companion.md —
   * and an empty key on save keeps the stored one.
   */
  key_set: boolean;
  /** What the model is told about the person, in their own words. */
  about: string;
  /** Offered as the placeholder, so the model name is not written down in two languages. */
  default_model: string;
  about_limit: number;
};

export type CompanionEdit = {
  api_key: string;
  model: string;
  about: string;
};

/** What answered, which is not always what was asked for. */
export type CompanionTest = {
  model: string;
  tokens: number;
};

export const getCompanion = () => request<Companion>("/api/companion");

export const putCompanion = (companion: CompanionEdit) =>
  request<Companion>("/api/companion", { method: "PUT", body: companion });

export const deleteCompanion = () =>
  request<void>("/api/companion", { method: "DELETE" });

/**
 * Tries what is in the form, not what was last saved.
 *
 * An empty `api_key` means the stored one, under the same rule a save follows — so what was
 * tried is what saving would store.
 */
export const postCompanionTest = (attempt: {
  api_key: string;
  model: string;
}) =>
  request<CompanionTest>("/api/companion/test", {
    method: "POST",
    body: attempt,
  });
