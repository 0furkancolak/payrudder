import type { components } from "@/shared/api/schema";

type GoLive = components["schemas"]["GoLive"];
type Item = NonNullable<GoLive["items"]>[number];

/** The items of the checklist in the order the server returns them; the console knows these five and ignores a key it does not know. */
export const GO_LIVE_ITEMS = ["email_verified", "legal_profile", "payment_provider", "plan_published", "terms_accepted"] as const;
export type GoLiveKey = (typeof GO_LIVE_ITEMS)[number];

export function isGoLiveKey(k: unknown): k is GoLiveKey {
  return (GO_LIVE_ITEMS as readonly unknown[]).includes(k);
}

/** The items the console can name, in order, each with whether it is done. A missing item counts as not done, so a short answer never reads as complete. */
export function checklist(g: GoLive | null | undefined): { key: GoLiveKey; done: boolean }[] {
  const byKey = new Map<string, Item>((g?.items ?? []).map((i) => [i.key ?? "", i]));
  return GO_LIVE_ITEMS.map((key) => ({ key, done: byKey.get(key)?.done === true }));
}

/** How many of the five are done, for a card that says "3 of 5". */
export function progress(g: GoLive | null | undefined): { done: number; total: number } {
  const items = checklist(g);
  return { done: items.filter((i) => i.done).length, total: items.length };
}

/** The first item that is not done, or null: what a card should point to next. */
export function nextStep(g: GoLive | null | undefined): GoLiveKey | null {
  return checklist(g).find((i) => !i.done)?.key ?? null;
}

/**
 * The payment scenarios of a sandbox customer, the same list the API serves at /v1/sandbox/scenarios (the gateway is the source; a test in the gateway
 * keeps its list at six). The console describes them in its own words per language, keyed by `scenario`.
 */
export const SCENARIOS = [
  { scenario: "success", outcome: "paid", default: true },
  { scenario: "declined", outcome: "uncollectible" },
  { scenario: "insufficient_funds_once", outcome: "paid_after_retry" },
  { scenario: "error_then_success", outcome: "paid_after_retry" },
  { scenario: "timeout_committed", outcome: "paid_after_status_query" },
  { scenario: "timeout_not_committed", outcome: "paid_after_recharge" },
] as const;
