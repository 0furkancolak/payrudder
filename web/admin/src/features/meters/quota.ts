import type { components } from "@/shared/api/schema";

export type Meter = components["schemas"]["Meter"];
export type Aggregation = NonNullable<Meter["aggregation"]>;
export type QuotaWindow = components["schemas"]["QuotaWindow"];
export type QuotaMode = components["schemas"]["QuotaMode"];
export type EntitlementOverride = components["schemas"]["EntitlementOverride"];
export type Entitlement = components["schemas"]["Entitlement"];

/** The terms of one quota as a person types them: the limit, the window it counts over, what reaching it does, and the alert thresholds. */
export interface QuotaDraft {
  limit: string;
  window: QuotaWindow;
  mode: QuotaMode;
  /** "50, 80, 100"; empty is the default (80 and 100). */
  thresholds: string;
}

/** A fresh quota: the billing month, hard, the default alerts. */
export const newQuota = (): QuotaDraft => ({ limit: "", window: "QUOTA_WINDOW_MONTH", mode: "QUOTA_MODE_HARD", thresholds: "" });

/**
 * The aggregations the engine computes, each exactly by the aggregator, limits-svc and rating: summed, counted, maximised, the number of distinct values of
 * a property (UNIQUE) or the quantity of the latest event of the period (LAST).
 */
export const AGGREGATIONS: readonly Aggregation[] = ["AGGREGATION_SUM", "AGGREGATION_COUNT", "AGGREGATION_MAX", "AGGREGATION_UNIQUE", "AGGREGATION_LAST"];

/** UNIQUE keeps a set of values and LAST the winning event beside the totals, so a code cannot change into or out of them in a later version. */
export const isRowShaped = (a: Aggregation | undefined): boolean => a === "AGGREGATION_UNIQUE" || a === "AGGREGATION_LAST";

/** The aggregations the next version of a meter may take: any for a new meter, its own for a UNIQUE or LAST one, and SUM, COUNT or MAX for the others. */
export function aggregationsFor(base?: Aggregation): readonly Aggregation[] {
  if (!base) return AGGREGATIONS;
  return isRowShaped(base) ? [base] : AGGREGATIONS.filter((a) => !isRowShaped(a));
}

/** The windows a quota can count over, shortest first. The month is the billing period (the default); a lifetime quota never starts again. */
export const WINDOWS: readonly QuotaWindow[] = [
  "QUOTA_WINDOW_HOUR",
  "QUOTA_WINDOW_DAY",
  "QUOTA_WINDOW_WEEK",
  "QUOTA_WINDOW_MONTH",
  "QUOTA_WINDOW_YEAR",
  "QUOTA_WINDOW_LIFETIME",
];

/** What reaching the limit does to `allowed`: hard stops allowing, soft keeps allowing and bills the excess at the price, notify only alerts. */
export const MODES: readonly QuotaMode[] = ["QUOTA_MODE_HARD", "QUOTA_MODE_SOFT", "QUOTA_MODE_NOTIFY"];

/** Units offered as suggestions; the field takes anything up to 32 characters. */
export const UNIT_SUGGESTIONS = ["calls", "requests", "tokens", "messages", "events", "GB", "GB-h", "minutes", "hours", "seats", "users", "documents"] as const;

/** The last word of an enum value, which the locale files key their labels by: "QUOTA_WINDOW_DAY" -> "day", unspecified -> the default. */
export function windowWord(w: QuotaWindow | string | undefined): string {
  const word = (w ?? "").replace("QUOTA_WINDOW_", "").toLowerCase();
  return word === "" || word === "unspecified" ? "month" : word;
}

export function modeWord(m: QuotaMode | string | undefined): string {
  const word = (m ?? "").replace("QUOTA_MODE_", "").toLowerCase();
  return word === "" || word === "unspecified" ? "hard" : word;
}

/**
 * Reads alert thresholds a person typed ("50, 80, 100" or "50 80 100") into the percentages the API takes, or null when they are not 1 to 10 strictly
 * ascending whole percentages between 1 and 1000. Empty is [] (the server's default: 80 and 100).
 */
export function parseThresholds(input: string): number[] | null {
  const parts = input
    .split(/[\s,;]+/)
    .map((p) => p.replace(/%$/, ""))
    .filter((p) => p !== "");
  if (parts.length === 0) return [];
  if (parts.length > 10 || parts.some((p) => !/^\d{1,4}$/.test(p))) return null;
  const out = parts.map(Number);
  for (let i = 0; i < out.length; i++) {
    const v = out[i] ?? 0;
    if (v < 1 || v > 1000 || (i > 0 && v <= (out[i - 1] ?? 0))) return null;
  }
  return out;
}

/** Whether text is a non-negative decimal quantity with at most `scale` decimal places ("1000", "2.5"), what the API takes for limits and quantities. */
export function isQuantity(text: string, scale: number): boolean {
  const m = /^(\d{1,15})(?:\.(\d+))?$/.exec(text.trim());
  if (!m) return false;
  return (m[2] ?? "").replace(/0+$/, "").length <= scale;
}

/** The meter's decimal places, 0 for a meter the list does not hold. */
export function scaleOf(meters: readonly Meter[], code: string): number {
  return meters.find((m) => m.code === code)?.quantity_scale ?? 0;
}

/** The categories in use, sorted, for the filter and the suggestions of the category field. */
export function categories(meters: readonly Meter[]): string[] {
  return [...new Set(meters.map((m) => (m.category ?? "").trim()).filter((c) => c !== ""))].sort((a, b) => a.localeCompare(b));
}

export type QuotaTerms = components["schemas"]["EntitlementTerms"];

/** A quota draft as the API's terms, or which field is wrong: the limit (not a quantity with the meter's decimals) or the thresholds. */
export function quotaTerms(d: QuotaDraft, scale: number): { terms: QuotaTerms } | { error: "limit" | "thresholds" } {
  const limit = d.limit.trim();
  if (!isQuantity(limit, scale)) return { error: "limit" };
  const thresholds = parseThresholds(d.thresholds);
  if (thresholds === null) return { error: "thresholds" };
  return { terms: { limit, window: d.window, mode: d.mode, ...(thresholds.length ? { alert_thresholds_pct: thresholds } : {}) } };
}

/** The terms the API sent back, as a draft to edit. */
export function quotaDraftOf(t: Partial<QuotaTerms>): QuotaDraft {
  return {
    limit: t.limit ?? "",
    window: t.window && t.window !== "QUOTA_WINDOW_UNSPECIFIED" ? t.window : "QUOTA_WINDOW_MONTH",
    mode: t.mode && t.mode !== "QUOTA_MODE_UNSPECIFIED" ? t.mode : "QUOTA_MODE_HARD",
    thresholds: (t.alert_thresholds_pct ?? []).join(", "),
  };
}
