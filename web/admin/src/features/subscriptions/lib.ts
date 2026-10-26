import type { Subscription } from "@/shared/api/client";

/** The billing intervals of a subscription, as the API names them. */
export const INTERVALS = ["SUBSCRIPTION_INTERVAL_MONTH", "SUBSCRIPTION_INTERVAL_YEAR"] as const;
export type Interval = (typeof INTERVALS)[number];

/** The locale key of an interval's word ("monthly", "yearly"); anything but a year reads as monthly. */
export function intervalKey(interval: string | undefined): "yearly" | "monthly" {
  return interval === "SUBSCRIPTION_INTERVAL_YEAR" ? "yearly" : "monthly";
}

/** What a priced change does with money now: nothing, a charge, or a credit. `net` is the quote's net in minor units as text; a leading minus is a credit. */
export function changeOutcome(net: string | undefined): { kind: "nothing" } | { kind: "credit" | "due"; minor: string } {
  if (net === undefined || net === "0") return { kind: "nothing" };
  return net.startsWith("-") ? { kind: "credit", minor: net.slice(1) } : { kind: "due", minor: net };
}

/**
 * What a person may do with the selected subscription, worked out once. A subscription a provider runs is changed through the provider; an ended one is
 * history; "advance" fees (paid at the start of the period) are the ones that can be changed with a quote, given a coupon, or ended with a credit.
 */
export function subscriptionAbilities(s: Pick<Subscription, "owner" | "end_time" | "fee_timing" | "coupon_code" | "state">) {
  const providerRuns = s.owner === "provider";
  const open = !s.end_time;
  const advance = s.fee_timing === "FEE_TIMING_ADVANCE";
  const active = s.state === "SUBSCRIPTION_STATE_ACTIVE";
  return {
    providerRuns,
    /** Quotify runs it and it has not ended: pause, cancel, change plan, move. */
    manageHere: !providerRuns && open,
    /** The quote-based change and the coupon apply to fees paid in advance. */
    advanceActions: !providerRuns && open && advance,
    /** Ending it now can give the unused part of an advance fee back as credit. */
    canEndWithCredit: open && advance && !s.coupon_code && active,
  };
}

/** How many days before its end a trial counts as "ending soon" on the subscriptions page. */
export const TRIAL_WARNING_DAYS = 7;

/** Whole days (rounded up) until a trialing subscription's trial ends: null when it is not in a trial, 0 when it ends today. */
export function trialDaysLeft(s: Pick<Subscription, "state" | "trial_end_time">, now: Date = new Date()): number | null {
  if (s.state !== "SUBSCRIPTION_STATE_TRIALING" || !s.trial_end_time) return null;
  const left = new Date(s.trial_end_time).getTime() - now.getTime();
  return Number.isNaN(left) ? null : Math.max(0, Math.ceil(left / 86_400_000));
}

/** The trials that end within `days` days, soonest first. */
export function trialsEndingSoon<S extends Pick<Subscription, "state" | "trial_end_time">>(list: readonly S[], now: Date = new Date(), days: number = TRIAL_WARNING_DAYS): S[] {
  return list
    .filter((s) => {
      const left = trialDaysLeft(s, now);
      return left !== null && left <= days;
    })
    .sort((a, b) => new Date(a.trial_end_time ?? 0).getTime() - new Date(b.trial_end_time ?? 0).getTime());
}
