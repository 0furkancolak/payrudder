import type { NewMeter } from "./api";

export type Aggregation = NewMeter["aggregation"];
export const AGGREGATIONS: Aggregation[] = ["AGGREGATION_SUM", "AGGREGATION_COUNT", "AGGREGATION_MAX", "AGGREGATION_UNIQUE", "AGGREGATION_LAST"];

/** UNIQUE keeps a set of values and LAST the winning event beside the totals, so a code cannot change into or out of them in a later version. */
export const isRowShaped = (a: Aggregation | undefined): boolean => a === "AGGREGATION_UNIQUE" || a === "AGGREGATION_LAST";

/** The aggregations the next version of a meter may take: any for a new meter, its own for a UNIQUE or LAST one, and SUM, COUNT or MAX for the others. */
export function aggregationsFor(base?: Aggregation): Aggregation[] {
  if (!base) return AGGREGATIONS;
  return isRowShaped(base) ? [base] : AGGREGATIONS.filter((a) => !isRowShaped(a));
}

/** Lowercase letters, digits and _, starting with a letter, at most 63 characters (the server's rule for a meter code). */
export const CODE_PATTERN = /^[a-z][a-z0-9_]{0,62}$/;
const PROPERTY = /^[A-Za-z_][A-Za-z0-9_.-]{0,63}$/;
export const MAX_SCALE = 6;

export interface MeterForm {
  code: string;
  unit: string;
  aggregation: Aggregation;
  property: string;
  scale: string;
  filters: { property: string; values: string }[];
}

export const emptyMeterForm = (): MeterForm => ({ code: "", unit: "", aggregation: "AGGREGATION_SUM", property: "", scale: "0", filters: [] });

/** The form of a meter that exists, for saving its next version. */
export function formOfMeter(m: { code: string; unit_label: string; aggregation: Aggregation; value_property?: string; quantity_scale?: number; filter?: Record<string, { values?: string[] }> }): MeterForm {
  return {
    code: m.code,
    unit: m.unit_label,
    aggregation: m.aggregation,
    property: m.value_property ?? "",
    scale: String(m.quantity_scale ?? 0),
    filters: Object.entries(m.filter ?? {}).map(([property, f]) => ({ property, values: (f.values ?? []).join(", ") })),
  };
}

export type MeterError = "code" | "unit" | "property" | "propertyUnique" | "scale" | "uniqueScale" | "filter";

/** The request of a form, or the field that is wrong (a key under billing.usage.meters.errors). The server checks the same rules; this saves the round trip. */
export function toMeterBody(f: MeterForm): { body: NewMeter } | { error: MeterError } {
  const code = f.code.trim();
  if (!CODE_PATTERN.test(code)) return { error: "code" };
  const unit = f.unit.trim();
  if (unit === "" || unit.length > 32) return { error: "unit" };
  const property = f.property.trim();
  if (f.aggregation !== "AGGREGATION_COUNT" && !PROPERTY.test(property)) return { error: f.aggregation === "AGGREGATION_UNIQUE" ? "propertyUnique" : "property" };
  if (!/^\d$/.test(f.scale.trim()) || Number(f.scale) > MAX_SCALE) return { error: "scale" };
  if (f.aggregation === "AGGREGATION_UNIQUE" && Number(f.scale) !== 0) return { error: "uniqueScale" };
  const filter: Record<string, { values: string[] }> = {};
  const rows = f.filters.filter((r) => r.property.trim() !== "" || r.values.trim() !== "");
  if (rows.length > 10) return { error: "filter" };
  for (const r of rows) {
    const values = [...new Set(r.values.split(",").map((v) => v.trim()).filter(Boolean))];
    if (!PROPERTY.test(r.property.trim()) || values.length === 0 || values.length > 50 || filter[r.property.trim()]) return { error: "filter" };
    filter[r.property.trim()] = { values };
  }
  return {
    body: {
      code,
      aggregation: f.aggregation,
      unit_label: unit,
      quantity_scale: Number(f.scale),
      ...(f.aggregation !== "AGGREGATION_COUNT" ? { value_property: property } : {}),
      ...(rows.length > 0 ? { filter } : {}),
    },
  };
}

/** The short word of an aggregation ("AGGREGATION_SUM" is "sum"). */
export const aggregationKey = (a: string | undefined): string => (a ?? "").replace("AGGREGATION_", "").toLowerCase();
