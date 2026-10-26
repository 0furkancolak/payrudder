import { keepPreviousData, useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";
import { bounds, fillBuckets, granularityFor, previousRange, type Bucket, type Range } from "./lib";

export type Meter = components["schemas"]["Meter"];
export type NewMeter = components["schemas"]["CreateMeterRequest"];
export type EntitlementStatus = components["schemas"]["EntitlementStatus"];

export const usageKeys = {
  meters: (tenantId: string) => ["tenants", tenantId, "meters"] as const,
  series: (tenantId: string, meter: string, r: Range, compare: boolean, customer: string) => ["tenants", tenantId, "usage-series", meter, r.from, r.to, compare, customer] as const,
  top: (tenantId: string, meter: string, period: string) => ["tenants", tenantId, "usage-top", meter, period] as const,
  entitlements: (tenantId: string, customer: string, period: string) => ["tenants", tenantId, "entitlements", customer, period] as const,
  customer: (tenantId: string, customer: string, period: string) => ["tenants", tenantId, "customer-usage", customer, period] as const,
};

/** The latest version of every meter. */
export function useMeters(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => usageKeys.meters(toValue(tenantId))),
    queryFn: async () => (await unwrap(tenantApi(toValue(tenantId)).GET("/v1/meters"))).meters ?? [],
  });
}

/** Creates a meter, or the next version of an existing code (the API does both with the same call). */
export function useSaveMeter(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: NewMeter) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/meters", { body })),
    onSuccess: () => queries.invalidateQueries({ queryKey: usageKeys.meters(toValue(tenantId)) }),
  });
}

export interface Series {
  current: Bucket[];
  /** The range of the same length before; absent when the comparison is off. */
  previous?: Bucket[];
  precision?: string;
  scale: number;
}

/** The usage series of one meter in a range (and the range before it), every bucket present. `customer` narrows it to one customer. */
export function seriesOptions(tenantId: string, meter: string, range: Range, compare: boolean, customer = "") {
  const g = granularityFor(range);
  const fetchOne = async (r: Range) => {
    const b = bounds(r);
    const res = await unwrap(
      tenantApi(tenantId).GET("/v1/analytics/usage", {
        params: { query: { meter, from: b.from, to: b.to, granularity: g, ...(customer ? { customer } : {}) } },
      }),
    );
    return { buckets: fillBuckets(res.points, r, g), precision: res.precision, scale: res.quantity_scale ?? 0 };
  };
  return {
    queryKey: usageKeys.series(tenantId, meter, range, compare, customer),
    queryFn: async (): Promise<Series> => {
      const [cur, prev] = await Promise.all([fetchOne(range), compare ? fetchOne(previousRange(range)) : Promise.resolve(undefined)]);
      return { current: cur.buckets, ...(prev ? { previous: prev.buckets } : {}), ...(cur.precision ? { precision: cur.precision } : {}), scale: cur.scale };
    },
    staleTime: 30_000,
  };
}

export function useSeries(tenantId: MaybeRefOrGetter<string>, meter: MaybeRefOrGetter<string>, range: MaybeRefOrGetter<Range>, compare: MaybeRefOrGetter<boolean>, customer: MaybeRefOrGetter<string> = "") {
  return useQuery({
    staleTime: 30_000,
    queryKey: computed(() => usageKeys.series(toValue(tenantId), toValue(meter), toValue(range), toValue(compare), toValue(customer))),
    queryFn: () => seriesOptions(toValue(tenantId), toValue(meter), toValue(range), toValue(compare), toValue(customer)).queryFn(),
    enabled: computed(() => toValue(meter) !== ""),
    placeholderData: keepPreviousData,
  });
}

/** The series of several meters at once (the overview's cards and the meters table share them with the chart through the cache). */
export function useSeriesOf(tenantId: MaybeRefOrGetter<string>, meters: MaybeRefOrGetter<readonly string[]>, range: MaybeRefOrGetter<Range>, compare: MaybeRefOrGetter<boolean>) {
  return useQueries({
    queries: computed(() => toValue(meters).map((m) => ({ ...seriesOptions(toValue(tenantId), m, toValue(range), toValue(compare)), placeholderData: keepPreviousData }))),
    combine: (results) => ({
      by: Object.fromEntries(results.map((r, i) => [toValue(meters)[i] ?? String(i), r.data])) as Record<string, Series | undefined>,
      pending: results.some((r) => r.isPending),
      error: results.find((r) => r.error)?.error ?? null,
    }),
  });
}

/** Customers ranked by a meter in a billing period. */
function topOptions(tenantId: string, meter: string, period: string, limit: number) {
  return {
    queryKey: [...usageKeys.top(tenantId, meter, period), limit],
    queryFn: async () => (await unwrap(tenantApi(tenantId).GET("/v1/analytics/top-customers", { params: { query: { meter, period, limit } } }))).customers ?? [],
    staleTime: 30_000,
  };
}

export function useTopCustomers(tenantId: MaybeRefOrGetter<string>, meter: MaybeRefOrGetter<string>, period: MaybeRefOrGetter<string>, limit = 10) {
  return useQuery({
    staleTime: 30_000,
    queryKey: computed(() => [...usageKeys.top(toValue(tenantId), toValue(meter), toValue(period)), limit]),
    queryFn: () => topOptions(toValue(tenantId), toValue(meter), toValue(period), limit).queryFn(),
    enabled: computed(() => toValue(meter) !== ""),
  });
}

/** The rankings of every meter (the heaviest `limit` customers each, up to 100). */
export function useTopOfAll(tenantId: MaybeRefOrGetter<string>, meters: MaybeRefOrGetter<readonly string[]>, period: MaybeRefOrGetter<string>, limit = 100) {
  return useQueries({
    queries: computed(() => toValue(meters).map((m) => topOptions(toValue(tenantId), m, toValue(period), limit))),
    combine: (results) => ({
      by: Object.fromEntries(results.map((r, i) => [toValue(meters)[i] ?? String(i), r.data ?? []])) as Record<string, NonNullable<(typeof results)[number]["data"]>>,
      pending: results.some((r) => r.isPending),
      error: results.find((r) => r.error)?.error ?? null,
    }),
  });
}

/** What customers have against their limits in a billing period: one call per customer, so the page asks only for the customers it shows. */
export function useEntitlements(tenantId: MaybeRefOrGetter<string>, customers: MaybeRefOrGetter<readonly string[]>, period: MaybeRefOrGetter<string>) {
  return useQueries({
    queries: computed(() =>
      toValue(customers).map((id) => ({
        queryKey: usageKeys.entitlements(toValue(tenantId), id, toValue(period)),
        queryFn: async () => (await unwrap(tenantApi(toValue(tenantId)).GET("/v1/customers/{external_id}/entitlements", { params: { path: { external_id: id }, query: { period: toValue(period) } } }))).items ?? [],
        staleTime: 30_000,
      })),
    ),
    combine: (results) => ({
      by: Object.fromEntries(results.map((r, i) => [toValue(customers)[i] ?? String(i), r.data])) as Record<string, EntitlementStatus[] | undefined>,
      pending: results.some((r) => r.isPending),
      error: results.find((r) => r.error)?.error ?? null,
    }),
  });
}

/** One customer's usage per meter in a billing period. */
export function useCustomerUsage(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string>, period: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => usageKeys.customer(toValue(tenantId), toValue(customer), toValue(period))),
    queryFn: async () => (await unwrap(tenantApi(toValue(tenantId)).GET("/v1/customers/{external_id}/usage", { params: { path: { external_id: toValue(customer) }, query: { period: toValue(period) } } }))).usage ?? [],
    enabled: computed(() => toValue(customer) !== ""),
  });
}
