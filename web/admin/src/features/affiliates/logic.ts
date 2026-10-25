import { parsePercent, percentText } from "@/features/pricing/api";
import type { components } from "@/shared/api/schema";
import { minorToMajor, parseMajor } from "@/shared/lib/money";
import type { Tone } from "@/shared/ui/status-pill";

export type Program = components["schemas"]["AffiliateProgram"];
/** A partner. `link` (the public referral URL) is optional until every gateway sends it. */
export type Affiliate = components["schemas"]["Affiliate"] & { link?: string };
export type Commission = components["schemas"]["AffiliateCommission"];
export type Balance = components["schemas"]["AffiliateBalance"];
export type Funnel = components["schemas"]["AffiliateFunnel"];
export type Payout = components["schemas"]["AffiliatePayout"];
export type JournalEntry = components["schemas"]["AffiliateJournalEntry"];
export type Referral = components["schemas"]["AffiliateReferral"];

export const PROGRAM_NOT_SET = "affiliate.program.not_set";

// --- basis points and percentages

/** Basis points as the percentage a person edits: 1000 -> "10", 825 -> "8.25". */
export const bpsToPercent = (bps: number): string => percentText(bps);

/** The basis points of a typed percentage (0 to 100, at most two decimals), or null. Done on the text. */
export const percentToBps = (text: string): number | null => parsePercent(text);

// --- the program form

export const TERMS = ["first_invoices", "months", "lifetime"] as const;
export const PROGRAM_STATUSES = ["draft", "active", "paused"] as const;
export const COMMISSION_KINDS = ["percent_bps", "fixed"] as const;

/** What the program form holds: everything as the text a person types, so a half-typed value is never lost to a conversion. */
export interface ProgramForm {
  status: (typeof PROGRAM_STATUSES)[number];
  kind: (typeof COMMISSION_KINDS)[number];
  /** A percentage ("10", "8.25") for percent_bps, an amount in major units ("5.00") for fixed. */
  value: string;
  currency: string;
  term: (typeof TERMS)[number];
  termValue: string;
  windowDays: string;
  holdDays: string;
  /** Major units of `currency`. */
  minPayout: string;
  coupon: string;
  clickCap: string;
  landingUrl: string;
  autoApprove: boolean;
}

/** The form of a program that does not exist yet. */
export function defaultProgramForm(currency: string): ProgramForm {
  return { status: "draft", kind: "percent_bps", value: "10", currency, term: "lifetime", termValue: "", windowDays: "30", holdDays: "30", minPayout: "", coupon: "", clickCap: "1000", landingUrl: "", autoApprove: false };
}

/** The form of a stored program; `currency` is the fallback for a percentage program, which stores none. */
export function programToForm(p: Program | null | undefined, currency: string): ProgramForm {
  if (!p) return defaultProgramForm(currency);
  const cur = p.currency || currency;
  const fixed = p.commission_kind === "fixed";
  return {
    status: p.status ?? "draft",
    kind: fixed ? "fixed" : "percent_bps",
    value: fixed ? minorToMajor(p.commission_value ?? "0", cur) : bpsToPercent(Number(p.commission_value ?? 0)),
    currency: cur,
    term: p.commission_term ?? "lifetime",
    termValue: p.commission_term_value ? String(p.commission_term_value) : "",
    windowDays: String(p.attribution_window_days ?? 30),
    holdDays: String(p.hold_days ?? 0),
    minPayout: p.min_payout_minor && p.min_payout_minor !== "0" ? minorToMajor(p.min_payout_minor, cur) : "",
    coupon: p.referred_coupon_code ?? "",
    clickCap: String(p.daily_click_cap ?? 1000),
    landingUrl: p.landing_url ?? "",
    autoApprove: p.auto_approve ?? false,
  };
}

/** The first thing wrong with the form; the key is `affiliates.program.errors.<problem>`. */
export type FormProblem = "value" | "term" | "window" | "hold" | "minPayout" | "clickCap" | "landing";

const wholeIn = (text: string, min: number, max: number): number | null => {
  if (!/^\d{1,9}$/.test(text.trim())) return null;
  const n = Number(text);
  return n >= min && n <= max ? n : null;
};

/** The request body of the form, or the problem with it. Amounts are minor-unit strings, a percentage is basis points. */
export function formToProgram(f: ProgramForm): { body: Program } | { problem: FormProblem } {
  let value: string;
  if (f.kind === "percent_bps") {
    const bps = percentToBps(f.value);
    if (bps === null || bps === 0) return { problem: "value" };
    value = String(bps);
  } else {
    const minor = parseMajor(f.value, f.currency);
    if (minor === null || minor === "0") return { problem: "value" };
    value = minor;
  }
  let termValue: number | undefined;
  if (f.term !== "lifetime") {
    const n = wholeIn(f.termValue, 1, 1200);
    if (n === null) return { problem: "term" };
    termValue = n;
  }
  const windowDays = wholeIn(f.windowDays, 1, 365);
  if (windowDays === null) return { problem: "window" };
  const holdDays = wholeIn(f.holdDays, 0, 180);
  if (holdDays === null) return { problem: "hold" };
  let minPayout = "0";
  if (f.minPayout.trim() !== "") {
    const m = parseMajor(f.minPayout, f.currency);
    if (m === null) return { problem: "minPayout" };
    minPayout = m;
  }
  const clickCap = wholeIn(f.clickCap, 1, 100000);
  if (clickCap === null) return { problem: "clickCap" };
  const landing = f.landingUrl.trim();
  if (landing !== "" && !/^https?:\/\/\S+$/i.test(landing)) return { problem: "landing" };
  return {
    body: {
      status: f.status,
      commission_kind: f.kind,
      commission_value: value,
      ...(f.kind === "fixed" ? { currency: f.currency } : {}),
      commission_term: f.term,
      ...(termValue !== undefined ? { commission_term_value: termValue } : {}),
      attribution_window_days: windowDays,
      hold_days: holdDays,
      min_payout_minor: minPayout,
      ...(f.coupon ? { referred_coupon_code: f.coupon } : {}),
      daily_click_cap: clickCap,
      landing_url: landing,
      auto_approve: f.autoApprove,
    },
  };
}

// --- money by currency (never added across currencies)

const big = (v: string | undefined): bigint => (v && /^-?\d+$/.test(v) ? BigInt(v) : 0n);

export interface CurrencyBalance {
  currency: string;
  pending: string;
  approved: string;
  paid: string;
  reversed: string;
}

/** The balances of all partners added up per currency, currencies sorted by code. */
export function sumBalances(rows: readonly Balance[] | undefined): CurrencyBalance[] {
  const by = new Map<string, { pending: bigint; approved: bigint; paid: bigint; reversed: bigint }>();
  for (const r of rows ?? []) {
    const cur = r.currency ?? "";
    const t = by.get(cur) ?? { pending: 0n, approved: 0n, paid: 0n, reversed: 0n };
    t.pending += big(r.pending_minor);
    t.approved += big(r.approved_minor);
    t.paid += big(r.paid_minor);
    t.reversed += big(r.reversed_minor);
    by.set(cur, t);
  }
  return [...by.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([currency, t]) => ({ currency, pending: String(t.pending), approved: String(t.approved), paid: String(t.paid), reversed: String(t.reversed) }));
}

/** The balance rows of each partner, by partner id. */
export function balancesByAffiliate(rows: readonly Balance[] | undefined): Map<string, Balance[]> {
  const out = new Map<string, Balance[]>();
  for (const r of rows ?? []) {
    const id = r.affiliate_id ?? "";
    out.set(id, [...(out.get(id) ?? []), r]);
  }
  return out;
}

export interface FunnelStep {
  key: "clicks" | "referrals" | "paying";
  count: number;
  /** The share of the step before it, in percent with one decimal (null for the first step or when that step is empty). */
  rate: number | null;
}

/** The three counted steps of the funnel with the conversion from each step to the next. */
export function funnelSteps(f: Funnel | undefined): FunnelStep[] {
  const counts = [Number(f?.clicks ?? 0), Number(f?.referrals ?? 0), Number(f?.paying_customers ?? 0)];
  const keys = ["clicks", "referrals", "paying"] as const;
  return keys.map((key, i) => {
    const prev = i === 0 ? 0 : (counts[i - 1] ?? 0);
    return { key, count: counts[i] ?? 0, rate: i === 0 || prev === 0 ? null : Math.round(((counts[i] ?? 0) / prev) * 1000) / 10 };
  });
}

/** The commission line of the funnel per currency, sorted by code, with the amount kept after reversals. */
export function funnelCommissions(f: Funnel | undefined): { currency: string; earned: string; reversed: string; net: string }[] {
  return [...(f?.commissions ?? [])]
    .sort((a, b) => (a.currency ?? "").localeCompare(b.currency ?? ""))
    .map((c) => ({ currency: c.currency ?? "", earned: c.earned_minor ?? "0", reversed: c.reversed_minor ?? "0", net: String(big(c.earned_minor) - big(c.reversed_minor)) }));
}

export interface JournalTotal {
  currency: string;
  debit: string;
  credit: string;
  balanced: boolean;
}

/** The journal's totals per currency; a currency is balanced when its debits equal its credits. */
export function journalTotals(rows: readonly { currency?: string; debit_minor?: string; credit_minor?: string }[] | undefined): JournalTotal[] {
  return [...(rows ?? [])]
    .sort((a, b) => (a.currency ?? "").localeCompare(b.currency ?? ""))
    .map((r) => ({ currency: r.currency ?? "", debit: r.debit_minor ?? "0", credit: r.credit_minor ?? "0", balanced: big(r.debit_minor) === big(r.credit_minor) }));
}

// --- statuses

export function affiliateTone(status: string | undefined): Tone {
  return status === "active" ? "success" : status === "pending" ? "warning" : status === "suspended" ? "danger" : "neutral";
}

export function commissionTone(status: string | undefined): Tone {
  return status === "paid" ? "success" : status === "approved" ? "info" : status === "pending" ? "warning" : status === "reversed" ? "danger" : "neutral";
}

export function payoutTone(status: string | undefined): Tone {
  return status === "paid" ? "success" : status === "open" ? "info" : "neutral";
}

export function programTone(status: string | undefined): Tone {
  return status === "active" ? "success" : status === "paused" ? "warning" : "neutral";
}

/** A reversal row (a negative amount that nets a commission already paid). */
export const isReversal = (c: Pick<Commission, "kind">): boolean => c.kind === "reversal";

/** The reasons a payout batch is skipped, as the API names them; anything else shows as itself. */
export const SKIP_REASONS = ["below_minimum", "not_positive"] as const;

// --- dates

/** A calendar day (2026-10-21) as the start of that UTC day, the `from` of a list. */
export const dayStart = (day: string): string | undefined => (/^\d{4}-\d{2}-\d{2}$/.test(day) ? `${day}T00:00:00Z` : undefined);

/** A calendar day as the start of the NEXT UTC day: the API's `to` is exclusive, so the picked day itself is included. */
export function dayEnd(day: string): string | undefined {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) return undefined;
  const d = new Date(`${day}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return undefined;
  d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString().replace(".000Z", "Z");
}
