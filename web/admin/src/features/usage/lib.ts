import type { ChartPoint } from "@/features/dashboard/lib";

/** A range of whole days, both ends included, as YYYY-MM-DD. The server counts UTC days, so the days here are UTC days. */
export interface Range {
  from: string;
  to: string;
}

const DAY_MS = 86_400_000;
const DAY = /^\d{4}-\d{2}-\d{2}$/;
const at = (day: string) => Date.parse(`${day}T00:00:00Z`);
const ymd = (ms: number) => new Date(ms).toISOString().slice(0, 10);

/** The longest range the page asks the series for: a year of daily points. */
export const MAX_DAYS = 366;

/** How many days a range covers (0 when it is not a range). */
export function rangeDays(r: Range): number {
  if (!DAY.test(r.from) || !DAY.test(r.to)) return 0;
  const n = Math.round((at(r.to) - at(r.from)) / DAY_MS) + 1;
  return Number.isFinite(n) && n > 0 ? n : 0;
}

export function validRange(r: Range): boolean {
  const n = rangeDays(r);
  return n >= 1 && n <= MAX_DAYS;
}

/** The range of the same length that ends the day before this one starts: what a period is compared with. */
export function previousRange(r: Range): Range {
  const n = rangeDays(r);
  const end = at(r.from) - DAY_MS;
  return { from: ymd(end - (n - 1) * DAY_MS), to: ymd(end) };
}

/** The billing period (a month, 2026-10) a day falls in: limits and rankings are counted per period. */
export const periodOf = (day: string): string => day.slice(0, 7);

/** The instants the series endpoint takes: the start of the first day and the start of the day after the last. */
export function bounds(r: Range): { from: string; to: string } {
  return { from: `${r.from}T00:00:00Z`, to: `${ymd(at(r.to) + DAY_MS)}T00:00:00Z` };
}

export type Granularity = "hour" | "day";

/** A range of one or two days is drawn by the hour, anything longer by the day. */
export const granularityFor = (r: Range): Granularity => (rangeDays(r) <= 2 ? "hour" : "day");

/** The start of every bucket of a range, in order, as RFC 3339 instants. */
export function bucketStarts(r: Range, g: Granularity): string[] {
  const n = rangeDays(r);
  const step = g === "hour" ? 3_600_000 : DAY_MS;
  const count = g === "hour" ? n * 24 : n;
  const start = at(r.from);
  return Array.from({ length: count }, (_, i) => new Date(start + i * step).toISOString().replace(".000Z", "Z"));
}

const keyOf = (iso: string, g: Granularity) => (g === "hour" ? iso.slice(0, 13) : iso.slice(0, 10));

export interface RawPoint {
  start?: string;
  quantity?: string;
  event_count?: string;
}

export interface Bucket {
  start: string;
  quantity: string;
  events: number;
}

/** The points of an answer laid on every bucket of the range: a bucket the server left out (nothing happened) is zero, so two periods line up point by point. */
export function fillBuckets(points: readonly RawPoint[] | undefined, r: Range, g: Granularity): Bucket[] {
  const by = new Map<string, RawPoint>();
  for (const p of points ?? []) if (p.start) by.set(keyOf(p.start, g), p);
  return bucketStarts(r, g).map((start) => {
    const p = by.get(keyOf(start, g));
    return { start, quantity: p?.quantity ?? "0", events: Number(p?.event_count ?? 0) || 0 };
  });
}

// ---- exact decimals (quantities are decimal strings with the meter's scale; sums are done on the digits, not on floats)

function parts(q: string): { int: bigint; scale: number } | null {
  const m = /^(-?)(\d+)(?:\.(\d+))?$/.exec(q.trim());
  if (!m) return null;
  const frac = m[3] ?? "";
  const int = BigInt(`${m[2]}${frac}`);
  return { int: m[1] === "-" ? -int : int, scale: frac.length };
}

function text(int: bigint, scale: number): string {
  const neg = int < 0n;
  const digits = (neg ? -int : int).toString().padStart(scale + 1, "0");
  const whole = digits.slice(0, digits.length - scale);
  return `${neg ? "-" : ""}${whole}${scale > 0 ? `.${digits.slice(digits.length - scale)}` : ""}`;
}

/** The exact sum of decimal strings; anything that is not a number counts as zero. */
export function sumDecimals(values: readonly (string | undefined)[]): string {
  const ps = values.map((v) => parts(v ?? "") ?? { int: 0n, scale: 0 });
  const scale = Math.max(0, ...ps.map((p) => p.scale));
  const sum = ps.reduce((n, p) => n + p.int * 10n ** BigInt(scale - p.scale), 0n);
  return text(sum, scale);
}

/** The largest of decimal strings; "0" for none. */
export function maxDecimal(values: readonly (string | undefined)[]): string {
  return values.reduce<string>((m, v) => (v !== undefined && compareDecimals(v, m) > 0 ? v : m), "0");
}

/** What a period's buckets add up to: a meter that takes the highest value (MAX) is the highest bucket, any other is the sum. */
export function totalOf(aggregation: string | undefined, quantities: readonly (string | undefined)[]): string {
  return aggregation === "AGGREGATION_MAX" ? maxDecimal(quantities) : sumDecimals(quantities);
}

/** -1, 0 or 1 like a comparator; a value that is not a number is zero. */
export function compareDecimals(a: string, b: string): number {
  const x = parts(a) ?? { int: 0n, scale: 0 };
  const y = parts(b) ?? { int: 0n, scale: 0 };
  const scale = Math.max(x.scale, y.scale);
  const l = x.int * 10n ** BigInt(scale - x.scale);
  const r = y.int * 10n ** BigInt(scale - y.scale);
  return l < r ? -1 : l > r ? 1 : 0;
}

/** How much of a limit is used, as a percentage with one decimal; null when there is no limit to measure against. A limit of zero with anything used is 100. */
export function percentUsed(used: string, limit: string | null | undefined): number | null {
  if (limit === null || limit === undefined || limit === "") return null;
  const u = parts(used);
  const l = parts(limit);
  if (!u || !l) return null;
  const scale = Math.max(u.scale, l.scale);
  const uu = u.int * 10n ** BigInt(scale - u.scale);
  const ll = l.int * 10n ** BigInt(scale - l.scale);
  if (ll <= 0n) return uu > 0n ? 100 : 0;
  return Number((uu * 1000n) / ll) / 10;
}

export type LimitState = "unlimited" | "ok" | "near" | "over";

export interface Entitlement {
  used?: string;
  limit?: string | null;
  unlimited?: boolean;
  allowed?: boolean;
}

/** Where a customer stands against a limit: unlimited, under 80 %, at or above 80 % (near) or at the limit (over). */
export function limitState(e: Entitlement): LimitState {
  if (e.unlimited || e.limit === null || e.limit === undefined) return "unlimited";
  if (e.allowed === false) return "over";
  const p = percentUsed(e.used ?? "0", e.limit);
  if (p === null) return "unlimited";
  return p >= 100 ? "over" : p >= 80 ? "near" : "ok";
}

const ORDER: Record<LimitState, number> = { over: 3, near: 2, ok: 1, unlimited: 0 };

/** The worst state among several: a customer over one limit is over. */
export function worstState(states: readonly LimitState[]): LimitState {
  return states.reduce<LimitState>((w, s) => (ORDER[s] > ORDER[w] ? s : w), "unlimited");
}

/** The chart's points: the series of the period, with the one before it laid under it point by point when there is one. */
export function chartPoints(current: readonly Bucket[], previous?: readonly Bucket[]): ChartPoint[] {
  return current.map((b, i) => {
    const p = previous?.[i];
    return { date: b.start, value: Number(b.quantity) || 0, ...(p ? { prevDate: p.start, prev: Number(p.quantity) || 0 } : {}) };
  });
}

/** The values alone, for a sparkline. */
export const values = (buckets: readonly Bucket[]): number[] => buckets.map((b) => Number(b.quantity) || 0);

export interface CustomerLike {
  external_id?: string;
  name?: string;
  email?: string;
}

export interface TopEntry {
  customer_external_id?: string;
  quantity?: string;
  event_count?: string;
}

export interface UsageRow {
  externalId: string;
  name: string;
  email: string;
  /** Usage in the period per meter code; a meter the customer did not use is absent. */
  usage: Record<string, { quantity: string; events: number }>;
}

/**
 * The customers of the tenant with what the rankings say they used. The rankings list only customers who used a meter, so every other customer has no
 * usage, and a ranked customer the list does not know (it was cut at its page) is added so no usage is lost. Most active first, then by id.
 */
export function mergeCustomers(customers: readonly CustomerLike[], tops: Readonly<Record<string, readonly TopEntry[]>>): UsageRow[] {
  const rows = new Map<string, UsageRow>();
  for (const c of customers) {
    if (c.external_id) rows.set(c.external_id, { externalId: c.external_id, name: c.name ?? "", email: c.email ?? "", usage: {} });
  }
  for (const [meter, list] of Object.entries(tops)) {
    for (const e of list) {
      const id = e.customer_external_id;
      if (!id) continue;
      const row = rows.get(id) ?? { externalId: id, name: "", email: "", usage: {} };
      row.usage[meter] = { quantity: e.quantity ?? "0", events: Number(e.event_count ?? 0) || 0 };
      rows.set(id, row);
    }
  }
  const events = (r: UsageRow) => Object.values(r.usage).reduce((n, u) => n + u.events, 0);
  return [...rows.values()].sort((a, b) => events(b) - events(a) || a.externalId.localeCompare(b.externalId));
}

/** A quantity (decimal string) as text in the reader's locale, without the zeros the meter's scale pads it with ("1500.250" is "1,500.25"). */
export function formatQuantity(q: string | undefined, locale: string): string {
  if (q === undefined || q === "") return "—";
  const p = parts(q);
  if (!p) return q;
  const trimmed = q.includes(".") ? q.replace(/0+$/, "").replace(/\.$/, "") : q;
  const fraction = trimmed.includes(".") ? trimmed.length - trimmed.indexOf(".") - 1 : 0;
  try {
    return new Intl.NumberFormat(locale, { minimumFractionDigits: 0, maximumFractionDigits: Math.min(fraction, 20) }).format(trimmed as unknown as number);
  } catch {
    return trimmed;
  }
}

/** A short form for a chart's axis (1.2K, 3.4M). */
export function formatCompactNumber(n: number, locale: string): string {
  try {
    return new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: 1 }).format(n);
  } catch {
    return String(n);
  }
}
