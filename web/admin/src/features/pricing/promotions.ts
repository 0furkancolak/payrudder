import { parseMajor } from "@/shared/lib/money";
import type { CodeTemplate, CouponTarget } from "./api";

/** A promotion code is 2 to 40 letters, digits, - or _. The server compares it in any letter case. */
export const CODE_PATTERN = /^[A-Za-z0-9_-]{2,40}$/;

/** The codes in a text: split on new lines, commas, semicolons and spaces; empty entries dropped; the same code in another case counted once. */
export function splitCodes(text: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of text.split(/[\s,;]+/)) {
    const code = part.trim();
    if (code === "" || seen.has(code.toUpperCase())) continue;
    seen.add(code.toUpperCase());
    out.push(code);
  }
  return out;
}

/** The codes that are not 2 to 40 letters, digits, - or _. */
export function badCodes(codes: string[]): string[] {
  return codes.filter((c) => !CODE_PATTERN.test(c));
}

/** A comma separated list as an array without empty entries; null when nothing was typed. */
export function list(text: string): string[] | undefined {
  const items = text
    .split(/[,;\n]+/)
    .map((s) => s.trim())
    .filter(Boolean);
  return items.length ? items : undefined;
}

/** The CSV a person downloads after making codes: one code per line under a header. */
export function codesCsv(codes: string[]): string {
  return ["code", ...codes].join("\r\n") + "\r\n";
}

export interface CodeForm {
  max: string;
  expires: string;
  firstPurchase: boolean;
  onePerCustomer: boolean;
  minAmount: string;
  minCurrency: string;
  currencies: string;
  plans: string;
  packs: string;
  restore: boolean;
}

export const emptyCodeForm = (currency: string): CodeForm => ({
  max: "",
  expires: "",
  firstPurchase: false,
  onePerCustomer: false,
  minAmount: "",
  minCurrency: currency,
  currencies: "",
  plans: "",
  packs: "",
  restore: false,
});

/**
 * The template of a form, or the name of the field that is wrong (a key under billing.pricing.promotions.codes.errors). The expiry is the end of the chosen day in the
 * person's time zone; the amount is read as text, never through a float.
 */
export function toTemplate(f: CodeForm, now: Date = new Date()): { template: CodeTemplate } | { error: "max" | "expires" | "minAmount" | "currencies" } {
  const t: CodeTemplate = {};
  if (f.max.trim() !== "") {
    const n = Number(f.max);
    if (!Number.isInteger(n) || n < 1 || n > 1_000_000) return { error: "max" };
    t.max_redemptions = n;
  }
  if (f.expires !== "") {
    const end = new Date(`${f.expires}T23:59:59`);
    if (Number.isNaN(end.getTime()) || end.getTime() <= now.getTime()) return { error: "expires" };
    t.expire_time = end.toISOString();
  }
  if (f.minAmount.trim() !== "") {
    const minor = parseMajor(f.minAmount, f.minCurrency);
    if (minor === null || minor === "0") return { error: "minAmount" };
    t.min_amount_minor = minor;
    t.min_amount_currency = f.minCurrency;
  }
  const currencies = list(f.currencies)?.map((c) => c.toUpperCase());
  if (currencies && currencies.some((c) => !/^[A-Z]{3}$/.test(c))) return { error: "currencies" };
  if (currencies) t.allowed_currencies = currencies;
  const plans = list(f.plans);
  if (plans) t.allowed_plans = plans;
  const packs = list(f.packs);
  if (packs) t.allowed_packs = packs;
  if (f.firstPurchase) t.first_purchase_only = true;
  if (f.onePerCustomer) t.one_per_customer = true;
  if (f.restore) t.restore_on_reversal = true;
  return { template: t };
}

/** The targets a coupon works on, as the form lists them. A bonus coupon is for credit purchases only. */
export const TARGETS: { key: "subscription" | "credit_purchase" | "payment"; value: CouponTarget }[] = [
  { key: "subscription", value: "COUPON_TARGET_SUBSCRIPTION" },
  { key: "credit_purchase", value: "COUPON_TARGET_CREDIT_PURCHASE" },
  { key: "payment", value: "COUPON_TARGET_PAYMENT" },
];

/** The short name of a target of the API ("COUPON_TARGET_CREDIT_PURCHASE" is "credit_purchase"). */
export const targetKey = (v: string | undefined): string => (v ?? "").replace("COUPON_TARGET_", "").toLowerCase();
