import { LOW_CONFIDENCE, type ParsedReceipt } from "@/shared/lib/receipt/parse";
import type { ExpenseRule } from "./api";

/** The rule whose text the vendor contains (the longest text wins; a rule of an app only applies to that app's inbox, one without an app to every inbox). */
export function matchRule(rules: readonly ExpenseRule[], vendor: string, appId = ""): ExpenseRule | undefined {
  const v = vendor.trim().toLowerCase();
  if (v === "") return undefined;
  return rules
    .filter((r) => (r.vendor_match ?? "").trim() !== "" && v.includes((r.vendor_match ?? "").trim().toLowerCase()) && (!r.app_id || r.app_id === appId))
    .sort((a, b) => (b.vendor_match ?? "").length - (a.vendor_match ?? "").length)[0];
}

/** Whether `amount` is within the rule's tolerance of the amount it expects, in the rule's currency. Integer arithmetic only. */
export function withinTolerance(rule: ExpenseRule, amountMinor: number, currency: string): boolean {
  const expected = Number(rule.expected_amount_minor ?? 0);
  if (!(expected > 0) || currency !== rule.currency) return false;
  return Math.abs(amountMinor - expected) * 10000 <= expected * (rule.tolerance_bps ?? 0);
}

/** The tax inside an amount that includes it, at a rate in basis points (2000 = 20%): amount * rate / (10000 + rate), rounded. */
export function taxInside(amountMinor: number, taxBps: number): number {
  return taxBps > 0 ? Math.round((amountMinor * taxBps) / (10000 + taxBps)) : 0;
}

/**
 * May this document be recorded without a person looking at it? Only when a rule says so and everything the expense needs was read with enough confidence:
 * the vendor matched the rule, a date was found, and the amount (in the rule's currency) is within the tolerance of the expected one. Anything else waits for review.
 */
export function canAutoApprove(rule: ExpenseRule | undefined, parsed: ParsedReceipt): boolean {
  if (!rule?.auto_approve || !parsed.amountMinor || !parsed.date) return false;
  if (parsed.amountMinor.confidence < LOW_CONFIDENCE || parsed.date.confidence < LOW_CONFIDENCE) return false;
  return withinTolerance(rule, parsed.amountMinor.value, parsed.currency?.value ?? rule.currency ?? "");
}
