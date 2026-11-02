import { minorToMajor, parseMajor } from "@/shared/lib/money";

/**
 * Fee rules (docs/reconciliation.md#fee-rules): what a provider is expected to keep of a payment in a currency, entered by an operator because Payrudder knows no
 * provider's prices. The API takes the rate in hundredths of a percent (`rate_bps`, 290 is 2.9 %) and the fixed part in minor units of the rule's currency; a
 * person types a percentage and an amount. Everything is done on the text: no float.
 */

/** The shape the server accepts of a provider: a provider or an account key such as `stripe~eu` (pkg/payments/accountkey.go). */
const ACCOUNT_KEY = /^[a-z][a-z0-9_-]{0,31}(~[a-z0-9][a-z0-9-]{0,30})?$/;
export const validAccountKey = (key: string): boolean => ACCOUNT_KEY.test(key);

/** A percentage a person typed ("2.9", "2,9 %", "0.25") as hundredths of a percent, or null when it is not 0 to 100 with at most two decimals. */
export function parseRate(input: string): number | null {
  const m = /^(\d{1,3})(?:[.,](\d{1,2}))?$/.exec(input.trim().replace(/\s*%$/, ""));
  if (!m) return null;
  const bps = Number(m[1]) * 100 + Number((m[2] ?? "").padEnd(2, "0") || 0);
  return bps <= 10000 ? bps : null;
}

/** Hundredths of a percent as the text a person reads and types: 290 is "2.9", 275 is "2.75", 100 is "1", 0 is "0". The inverse of parseRate. */
export function rateText(bps: number | undefined): string {
  const n = bps ?? 0;
  const whole = Math.floor(n / 100);
  const frac = String(n % 100).padStart(2, "0").replace(/0+$/, "");
  return frac ? `${whole}.${frac}` : String(whole);
}

/** The fixed part a person typed in the rule's currency ("0.30") as minor units, or null when it is not an amount that currency has. Empty is no fixed part. */
export function parseFixed(input: string, currency: string): number | null {
  if (input.trim() === "") return 0;
  const minor = parseMajor(input, currency);
  const n = minor === null ? NaN : Number(minor);
  return Number.isSafeInteger(n) ? n : null;
}

/** Minor units of a currency as the text to edit ("30" cents is "0.30"); a rule with no fixed part shows an empty box. */
export const fixedText = (minor: number | undefined, currency: string): string => (minor ? minorToMajor(minor, currency) : "");

export interface RuleDraft {
  provider: string;
  currency: string;
  rate: string;
  fixed: string;
}

export interface RuleBody {
  provider: string;
  currency: string;
  rate_bps: number;
  fixed_minor: number;
}

/** Why a draft cannot be saved, as the key of the sentence under `billing.feeRules.errors`; null when it can. The server checks the same and the currency list. */
export function draftProblem(d: RuleDraft): "provider" | "currency" | "rate" | "fixed" | null {
  if (!validAccountKey(d.provider.trim())) return "provider";
  if (!/^[A-Za-z]{3}$/.test(d.currency.trim())) return "currency";
  if (parseRate(d.rate) === null) return "rate";
  if (parseFixed(d.fixed, d.currency.trim().toUpperCase()) === null) return "fixed";
  return null;
}

/** The body of the PUT for a valid draft (the currency in capitals, as the server stores it). */
export function draftBody(d: RuleDraft): RuleBody {
  const currency = d.currency.trim().toUpperCase();
  return { provider: d.provider.trim(), currency, rate_bps: parseRate(d.rate) ?? 0, fixed_minor: parseFixed(d.fixed, currency) ?? 0 };
}

/** The line a rule is named by in a list, a sheet and the address: environment, provider and currency are its key. */
export const ruleId = (r: { environment?: string; provider?: string; currency?: string }): string => `${r.environment ?? ""}:${r.provider ?? ""}:${r.currency ?? ""}`;

/** A rate for display in the reader's language ("2.9%", "2,9 %"): hundredths of a percent through Intl's percent format. For labels only. */
export const rateLabel = (bps: number | undefined, locale: string): string =>
  new Intl.NumberFormat(locale, { style: "percent", minimumFractionDigits: 0, maximumFractionDigits: 2 }).format((bps ?? 0) / 10000);
