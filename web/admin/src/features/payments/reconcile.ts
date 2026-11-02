import type { Tone } from "@/shared/ui/status-pill";

/** What the queue lists: what needs a person (open and explained), what is done, or everything. */
export const VIEWS = ["review", "resolved", "all"] as const;
export type View = (typeof VIEWS)[number];

/** The statuses the server is asked for; none means all. */
export function statusesOf(view: View): string[] {
  return view === "review" ? ["open", "explained"] : view === "resolved" ? ["resolved"] : [];
}

export const STATUSES = ["open", "explained", "resolved"] as const;
export const KINDS = [
  "paid_without_charge",
  "amount_mismatch",
  "duplicate_charge",
  "charge_without_paid",
  "conflicting_outcomes",
  "payment_disputed",
  "settlement_missing",
  "settlement_mismatch",
  "fee_mismatch",
] as const;
export const RESOLUTIONS = ["ledger_fact", "credit_note", "refunded_at_provider", "corrected_in_billing", "not_an_error", "other"] as const;

/**
 * The ways a discrepancy may be resolved. A ledger fact writes one row to the payment ledger, which the server allows for a payment billing calls paid that the
 * ledger lacks, and one at a time (it needs the provider's reference of that payment): so not for other kinds and not in bulk.
 */
export function resolutionsFor(kind: string | undefined, bulk: boolean): string[] {
  return RESOLUTIONS.filter((r) => r !== "ledger_fact" || (!bulk && kind === "paid_without_charge"));
}

/** Resolved ones are history; the rest can be explained, resolved and (all of them) given an owner. */
export const actionable = (d: { status?: string }): boolean => d.status !== "resolved";

export function statusTone(status: string | undefined): Tone {
  return status === "open" ? "warning" : status === "explained" ? "info" : status === "resolved" ? "success" : "neutral";
}

/** The billing periods a person can pick: this month and the eleven before it, newest first. */
export function recentPeriods(now: Date, n = 12): string[] {
  return Array.from({ length: n }, (_, i) => {
    const d = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - i, 1));
    return `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, "0")}`;
  });
}

/** The environment word of the API ("sandbox" or "live") for a tenant's ENVIRONMENT_* enum; undefined when the tenant is not loaded or says neither. */
export function environmentOf(env: string | undefined): "sandbox" | "live" | undefined {
  return env === "ENVIRONMENT_LIVE" ? "live" : env === "ENVIRONMENT_SANDBOX" ? "sandbox" : undefined;
}
