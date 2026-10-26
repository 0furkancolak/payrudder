import type { Call } from "./gateway";

/** The meters of the mocked tenant: a sum with no decimals and a max with three. */
export const METERS = [
  { code: "api_calls", aggregation: "AGGREGATION_SUM", value_property: "count", quantity_scale: 0, unit_label: "calls", version: 1, create_time: "2026-09-01T09:00:00Z" },
  { code: "storage_gb", aggregation: "AGGREGATION_MAX", value_property: "size", quantity_scale: 3, unit_label: "GB", version: 2, create_time: "2026-09-10T09:00:00Z", filter: { tier: { values: ["hot", "warm"] } } },
];

export const CUSTOMERS = [
  { id: "c1", external_id: "cus_busy", name: "Busy Co", email: "busy@example.com" },
  { id: "c2", external_id: "cus_near", name: "Near Ltd", email: "near@example.com" },
  { id: "c3", external_id: "cus_idle", name: "Idle Inc", email: "idle@example.com" },
];

/** Usage per meter and customer in the billing period (the ranking the API gives). */
const TOP: Record<string, { customer_external_id: string; quantity: string; event_count: string }[]> = {
  api_calls: [
    { customer_external_id: "cus_busy", quantity: "1200", event_count: "40" },
    { customer_external_id: "cus_near", quantity: "850", event_count: "30" },
  ],
  storage_gb: [{ customer_external_id: "cus_near", quantity: "2.500", event_count: "5" }],
};

/** What each customer is held to: busy is over, near is at 85 %, nobody else has a limit. */
const LIMITS: Record<string, { meter_code: string; used: string; limit: string }[]> = {
  cus_busy: [{ meter_code: "api_calls", used: "1200", limit: "1000" }],
  cus_near: [
    { meter_code: "api_calls", used: "850", limit: "1000" },
    { meter_code: "storage_gb", used: "2.500", limit: "10.000" },
  ],
};

/** The days of a series answer: 10 a day from the first of October on, 5 before, each with 2 events. */
function series(from: string, to: string, granularity: string) {
  const step = granularity === "hour" ? 3_600_000 : 86_400_000;
  const points: { start: string; quantity: string; event_count: string }[] = [];
  for (let t = Date.parse(from); t < Date.parse(to); t += step) {
    const start = new Date(t).toISOString().replace(".000Z", "Z");
    points.push({ start, quantity: start >= "2026-10-01" ? "10" : "5", event_count: "2" });
  }
  return points;
}

/** The reads of the usage pages: meters, series, rankings, limits. Returns nothing for other calls. */
export function usageReads(c: Call): { status?: number; body?: unknown } | undefined {
  if (c.method !== "GET") return undefined;
  if (c.path === "/v1/meters") return { body: { meters: METERS } };
  if (c.path === "/v1/customers") return { body: { customers: CUSTOMERS } };
  if (c.path === "/v1/analytics/usage") {
    const g = c.query.get("granularity") ?? "hour";
    return { body: { precision: "estimated", quantity_scale: 0, points: series(c.query.get("from") ?? "", c.query.get("to") ?? "", g) } };
  }
  if (c.path === "/v1/analytics/top-customers") return { body: { precision: "exact", customers: TOP[c.query.get("meter") ?? ""] ?? [] } };
  const ent = /^\/v1\/customers\/([^/]+)\/entitlements$/.exec(c.path);
  if (ent) {
    const items = (LIMITS[ent[1] ?? ""] ?? []).map((l) => ({ customer_external_id: ent[1], period_id: c.query.get("period"), unlimited: false, allowed: Number(l.used) < Number(l.limit), meter_code: l.meter_code, used: l.used, limit: l.limit }));
    return { body: { items } };
  }
  const use = /^\/v1\/customers\/([^/]+)\/usage$/.exec(c.path);
  if (use) {
    const id = use[1] ?? "";
    return { body: { customer_external_id: id, period_id: c.query.get("period"), usage: Object.entries(TOP).flatMap(([m, l]) => l.filter((x) => x.customer_external_id === id).map((x) => ({ meter_code: m, quantity: x.quantity, event_count: Number(x.event_count) }))) } };
  }
  return undefined;
}
