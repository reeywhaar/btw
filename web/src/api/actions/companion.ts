import { request } from "@app/api/transport";

/**
 * How the last round of questions went.
 *
 * Deliberately no counts — see docs/api_design.md. "some" is what somebody needs to know, and
 * "5 of 7 reminders" would be a number that goes up on a settings screen.
 */
export type Advice = {
  status: "none" | "some" | "all" | "limited" | "failed";
  /** Unix seconds, or null when no answer has ever arrived. */
  advised_at: number | null;
  attempted_at: number | null;
  /** The gateway's own words, when the last attempt failed. */
  error: string;
  /** Something has changed since the last answer, so another look is coming. */
  stale: boolean;
};

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
  /** Absent until a key is configured, because the loop does not run without one. */
  advice?: Advice;
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

/** One reminder as the weighting currently sees it. */
export type AdvisedReminder = {
  id: string;
  text: string;
  advised: boolean;
  categories?: string[];
  exclusive?: boolean;
  /**
   * Seven days of forty-eight numbers, Monday first, each 0 to 1. Absent when the companion
   * has said nothing about this reminder, or said something the weighting cannot read.
   */
  curve?: number[][];
  /**
   * What arrived when the curve could not be read — "7x24", "obj:6". Present only then, and
   * only so the screen can say which shape it refused rather than leaving somebody guessing.
   */
  shape?: string;
  advised_at?: number | null;
};

export type AdviceList = {
  reminders: AdvisedReminder[];
  /**
   * Whether an answer is still owed. Going false is the moment the drawing changed, which is
   * what lets a screen wait for a fresh look rather than telling somebody to come back.
   */
  stale: boolean;
  advised_at: number | null;
  /** Why the last attempt failed, when it did. */
  error: string;
  /** The shape, from the server, so the screen cannot disagree with it about the size. */
  days: number;
  windows: number;
};

export const getCompanionAdvice = () =>
  request<AdviceList>("/api/companion/advice");

/**
 * Asks the companion again, and waits for the answer.
 *
 * Slow on purpose — as slow as the model — and it comes back with the advice as it now stands,
 * so the screen redraws from the reply rather than asking again.
 */
export const postCompanionAdviceRefresh = () =>
  request<AdviceList>("/api/companion/advice/refresh", {
    method: "POST",
    body: {},
  });
