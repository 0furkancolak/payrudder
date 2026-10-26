import { formatMinor, minorDigits, parseMajor } from "@/shared/lib/money";
import { parsePercent, type CodeTemplate, type CouponTarget, type NewCoupon } from "./api";

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
    const end = endOfDay(f.expires, now);
    if (end === null) return { error: "expires" };
    t.expire_time = end;
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

/** The end of a day (YYYY-MM-DD) in the person's time zone as an instant; null when the text is not a day or the day is over. */
export function endOfDay(day: string, now: Date = new Date()): string | null {
  const end = new Date(`${day}T23:59:59`);
  return Number.isNaN(end.getTime()) || end.getTime() <= now.getTime() ? null : end.toISOString();
}

// ---- the form of a coupon

export type CouponKind = "percent" | "amount" | "bonus";

export interface CouponForm {
  code: string;
  name: string;
  kind: CouponKind;
  value: string;
  currency: string;
  targets: CouponTarget[];
  duration: NonNullable<NewCoupon["duration"]>;
  periods: string;
  max: string;
  expires: string;
  /** Also make the promotion code that has the coupon's own text (what the API does unless told not to). */
  defaultCode: boolean;
}

export const ALL_TARGETS: CouponTarget[] = TARGETS.map((x) => x.value);

export const emptyCouponForm = (currency: string): CouponForm => ({
  code: "",
  name: "",
  kind: "percent",
  value: "",
  currency,
  targets: [...ALL_TARGETS],
  duration: "COUPON_DURATION_ONCE",
  periods: "3",
  max: "",
  expires: "",
  defaultCode: true,
});

/** A bonus coupon is for credit purchases only; the others work wherever they are ticked. */
export const effectiveTargets = (f: Pick<CouponForm, "kind" | "targets">): CouponTarget[] => (f.kind === "bonus" ? ["COUPON_TARGET_CREDIT_PURCHASE"] : f.targets);

/** Whether the coupon works on subscriptions, which is when it has a duration. */
export const forSubscription = (f: Pick<CouponForm, "kind" | "targets">): boolean => effectiveTargets(f).includes("COUPON_TARGET_SUBSCRIPTION");

/** Basis points of a typed percentage of extra credits: 0.01 to 1000 with at most two decimals, done on the text. */
export function parseBonus(input: string): number | null {
  const m = /^(\d{1,4})(?:[.,](\d{1,2}))?$/.exec(input.trim());
  if (!m) return null;
  const bps = Number(m[1]) * 100 + Number((m[2] ?? "").padEnd(2, "0"));
  return bps <= 100000 ? bps : null;
}

export type CouponError = "code" | "value" | "targets" | "periods" | "max" | "expires";

/** The request of a form, or the field that is wrong (a key under billing.coupons.errors). */
export function toCouponBody(f: CouponForm, now: Date = new Date()): { body: NewCoupon } | { error: CouponError } {
  if (!CODE_PATTERN.test(f.code.trim())) return { error: "code" };
  const targets = effectiveTargets(f);
  if (targets.length === 0) return { error: "targets" };
  const body: NewCoupon = { code: f.code.trim(), name: f.name.trim() };
  // Left out when the coupon works everywhere (the default), and for a bonus coupon, which is for credit purchases only.
  if (f.kind !== "bonus" && targets.length < TARGETS.length) body.applies_to = targets;
  if (f.kind === "percent") {
    const bps = parsePercent(f.value);
    if (bps === null || bps === 0) return { error: "value" };
    body.percent_bps = bps;
  } else if (f.kind === "bonus") {
    const bps = parseBonus(f.value);
    if (bps === null || bps === 0) return { error: "value" };
    body.bonus_percent_bps = bps;
  } else {
    const minor = parseMajor(f.value, f.currency);
    if (minor === null || minor === "0") return { error: "value" };
    body.amount_minor = minor;
    body.currency = f.currency;
  }
  if (forSubscription(f)) {
    body.duration = f.duration;
    if (f.duration === "COUPON_DURATION_REPEATING") {
      const n = Number(String(f.periods).trim());
      if (!/^\d+$/.test(String(f.periods).trim()) || n < 1 || n > 120) return { error: "periods" };
      body.duration_periods = n;
    }
  }
  if (String(f.max).trim() !== "") {
    const n = Number(String(f.max).trim());
    if (!/^\d+$/.test(String(f.max).trim()) || n < 1) return { error: "max" };
    body.max_redemptions = n;
  }
  if (f.expires !== "") {
    const end = endOfDay(f.expires, now);
    if (end === null) return { error: "expires" };
    body.expire_time = end;
  }
  if (!f.defaultCode) body.without_default_code = true;
  return { body };
}

/** What a coupon does to an example, for the live line under the form: 100.00 of a fee, or 1,000 credits. */
export type Effect =
  | { kind: "percent"; bps: number; currency: string; before: string; after: string }
  | { kind: "amount"; currency: string; off: string; before: string; after: string }
  | { kind: "bonus"; bps: number; credits: number; bonus: number };

/** The example of a coupon's value, or null while the value is not readable. Amounts are minor-unit strings; the arithmetic is on integers. */
export function couponEffect(kind: CouponKind, value: string, currency: string): Effect | null {
  if (kind === "bonus") {
    const bps = parseBonus(value);
    return bps === null || bps === 0 ? null : { kind, bps, credits: 1000, bonus: Math.floor((1000 * bps) / 10000) };
  }
  const base = BigInt(minorOf100(currency));
  if (kind === "percent") {
    const bps = parsePercent(value);
    if (bps === null || bps === 0) return null;
    const off = (base * BigInt(bps)) / 10000n;
    return { kind, bps, currency, before: base.toString(), after: (base - off).toString() };
  }
  const minor = parseMajor(value, currency);
  if (minor === null || minor === "0") return null;
  const off = BigInt(minor);
  return { kind, currency, off: minor, before: base.toString(), after: (off >= base ? 0n : base - off).toString() };
}

/** 100.00 of a currency in its minor units ("10000" for dollars, "100" for yen). */
const minorOf100 = (currency: string): string => `100${"0".repeat(minorDigits(currency))}`;

/** What a coupon is worth, as a short text: "10 %", "$4.99", "25 % more credits". `t` and `locale` are the page's. */
export function describeCoupon(c: { percent_bps?: number; amount_minor?: string; currency?: string; bonus_percent_bps?: number }, t: (key: string, params?: Record<string, unknown>) => string, locale: string): string {
  const pct = (bps: number) => `${(bps / 100).toLocaleString(locale)} %`;
  if (c.bonus_percent_bps) return t("billing.pricing.promotions.bonusValue", { pct: pct(c.bonus_percent_bps) });
  if (c.percent_bps) return pct(c.percent_bps);
  return formatMinor(c.amount_minor, c.currency, locale);
}

/** How long a subscription keeps a coupon; a dash for a coupon that does not work on subscriptions (no targets listed means it works everywhere). */
export function describeDuration(c: { duration?: string; duration_periods?: number; applies_to?: string[] }, t: (key: string, params?: Record<string, unknown>) => string): string {
  const at = c.applies_to ?? [];
  if (at.length > 0 && !at.includes("COUPON_TARGET_SUBSCRIPTION")) return "—";
  return c.duration === "COUPON_DURATION_REPEATING"
    ? t("billing.pricing.coupons.repeatingFor", { n: c.duration_periods ?? 0 })
    : c.duration === "COUPON_DURATION_FOREVER"
      ? t("billing.pricing.coupons.forever")
      : t("billing.pricing.coupons.once");
}

/** The rules of a promotion code as short phrases, in the order the code table shows them. */
export function codeRules(
  c: {
    first_purchase_only?: boolean;
    one_per_customer?: boolean;
    min_amount_minor?: string;
    min_amount_currency?: string;
    allowed_plans?: string[];
    allowed_packs?: string[];
    allowed_currencies?: string[];
    restore_on_reversal?: boolean;
  },
  t: (key: string, params?: Record<string, unknown>) => string,
  locale: string,
): string[] {
  const out: string[] = [];
  if (c.first_purchase_only) out.push(t("billing.pricing.promotions.codes.rule.firstPurchase"));
  if (c.one_per_customer) out.push(t("billing.pricing.promotions.codes.rule.onePerCustomer"));
  if (c.min_amount_minor && c.min_amount_minor !== "0") out.push(t("billing.pricing.promotions.codes.rule.min", { amount: formatMinor(c.min_amount_minor, c.min_amount_currency, locale) }));
  if (c.allowed_currencies?.length) out.push(c.allowed_currencies.join(" "));
  if (c.allowed_plans?.length) out.push(t("billing.pricing.promotions.codes.rule.plans", { list: c.allowed_plans.join(", ") }));
  if (c.allowed_packs?.length) out.push(t("billing.pricing.promotions.codes.rule.packs", { n: c.allowed_packs.length }));
  if (c.restore_on_reversal) out.push(t("billing.pricing.promotions.codes.rule.restore"));
  return out;
}

/** Where a coupon works as words: "Everywhere" when it lists all three targets or none, else the targets by name. */
export function describeWhere(c: { applies_to?: string[] }, t: (key: string, params?: Record<string, unknown>) => string): string {
  const at = c.applies_to ?? [];
  if (at.length === 0 || at.length >= TARGETS.length) return t("billing.coupons.everywhere");
  return at.map((x) => t(`billing.pricing.promotions.targets.${targetKey(x)}`)).join(", ");
}
