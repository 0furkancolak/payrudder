import { percentText } from "@/features/pricing/api";
import type { components } from "@/shared/api/schema";
import type { Tone } from "@/shared/ui/status-pill";

type S = components["schemas"];
export type Affiliation = S["MyAffiliation"];
export type AffiliationDetail = S["MyAffiliationDetail"];
export type MyProgram = S["MyProgram"];
export type MyReferral = S["MyReferral"];
export type MyCommission = S["MyCommission"];
export type MyPayout = S["MyPayout"];

export { sumBalances } from "@/features/affiliates/logic";
export { affiliateTone, commissionTone, payoutTone } from "@/features/affiliates/logic";

/** A sandbox partner earns fake money; the page says so wherever the environment is shown. */
export const isSandbox = (a: Pick<Affiliation, "environment"> | undefined): boolean => a?.environment === "sandbox";

/** The tone of an environment badge: a sandbox is never mistaken for a live one. */
export const environmentTone = (env: string | undefined): Tone => (env === "sandbox" ? "warning" : "info");

/** Live affiliations first, then the newest; the API already orders them, this keeps the page stable if it did not. */
export function sortAffiliations(list: readonly Affiliation[] | undefined): Affiliation[] {
  return [...(list ?? [])].sort((a, b) => Number(isSandbox(a)) - Number(isSandbox(b)) || (b.create_time ?? "").localeCompare(a.create_time ?? ""));
}

const big = (v: string | undefined): bigint => (v && /^-?\d+$/.test(v) ? BigInt(v) : 0n);

/** What was earned in the period per currency, with what was taken back and what is left. Currencies are never added together. */
export function earnedByCurrency(rows: AffiliationDetail["earned"] | undefined): { currency: string; earned: string; reversed: string; net: string }[] {
  return [...(rows ?? [])]
    .sort((a, b) => (a.currency ?? "").localeCompare(b.currency ?? ""))
    .map((r) => ({ currency: r.currency ?? "", earned: r.earned_minor ?? "0", reversed: r.reversed_minor ?? "0", net: String(big(r.earned_minor) - big(r.reversed_minor)) }));
}

/** A negative row that nets a commission already paid. */
export const isReversal = (c: Pick<MyCommission, "kind">): boolean => c.kind === "reversal";

/** One line of the program's terms in words: an i18n key under `partner.terms` and its values. */
export interface TermLine {
  key: string;
  params: Record<string, string | number>;
}

/**
 * The program's rules for a partner, as lines of words: the commission (a percentage from basis points, or a fixed amount), for how long it is earned,
 * the attribution window, the hold and the minimum payout. `money` formats minor units in a currency; the minimum is left out when the program names
 * no currency to read it in.
 */
export function programTerms(p: MyProgram | null | undefined, money: (minor: string, currency: string) => string): TermLine[] {
  if (!p) return [];
  const lines: TermLine[] = [];
  if (p.commission_kind === "fixed") lines.push({ key: "fixed", params: { amount: money(p.commission_value ?? "0", p.currency ?? "") } });
  else lines.push({ key: "percent", params: { pct: percentText(Number(p.commission_value ?? 0)) } });
  if (p.commission_term === "first_invoices") lines.push({ key: "firstInvoices", params: { n: p.commission_term_value ?? 1 } });
  else if (p.commission_term === "months") lines.push({ key: "months", params: { n: p.commission_term_value ?? 1 } });
  else lines.push({ key: "lifetime", params: {} });
  lines.push({ key: "window", params: { days: p.attribution_window_days ?? 0 } });
  lines.push({ key: "hold", params: { days: p.hold_days ?? 0 } });
  if (p.min_payout_minor && p.min_payout_minor !== "0" && p.currency) lines.push({ key: "minPayout", params: { amount: money(p.min_payout_minor, p.currency) } });
  else lines.push({ key: "noMinimum", params: {} });
  return lines;
}

export { AFFILIATE_ROLE, hasPartnerRole, ownMemberships, safeNext } from "@/features/product/lib";
