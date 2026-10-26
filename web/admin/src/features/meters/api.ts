import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";
import type { EntitlementOverride, Meter } from "./quota";

export const meterKeys = {
  list: (tenantId: string) => ["tenants", tenantId, "meters"] as const,
  overrides: (tenantId: string, customer: string) => ["tenants", tenantId, "customers", customer, "entitlement-overrides"] as const,
  /** The same key as the subscriptions feature's usage query, which an override changes. */
  entitlements: (tenantId: string, customer: string) => ["tenants", tenantId, "entitlements", customer] as const,
};

/** The latest version of every meter of the tenant, by code. */
export function useMeters(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => meterKeys.list(toValue(tenantId))),
    queryFn: async (): Promise<Meter[]> => {
      const { meters = [] } = await unwrap(tenantApi(toValue(tenantId)).GET("/v1/meters"));
      return [...meters].sort((a, b) => (a.code ?? "").localeCompare(b.code ?? ""));
    },
    enabled: computed(() => toValue(tenantId) !== ""),
  });
}

export type NewMeter = components["schemas"]["CreateMeterRequest"];

/** Creates a meter, or the next version of an existing code (its decimal places stay what they were). */
export function useCreateMeter(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: NewMeter) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/meters", { body })),
    onSuccess: () => queries.invalidateQueries({ queryKey: meterKeys.list(toValue(tenantId)) }),
  });
}

/** A customer's own limits, which replace its package's on their meters. */
export function useOverrides(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => meterKeys.overrides(toValue(tenantId), toValue(customer))),
    queryFn: async (): Promise<EntitlementOverride[]> => {
      const r = await unwrap(
        tenantApi(toValue(tenantId)).GET("/v1/customers/{external_id}/entitlement-overrides", { params: { path: { external_id: toValue(customer) } } }),
      );
      return r.overrides ?? [];
    },
    enabled: computed(() => toValue(tenantId) !== "" && toValue(customer) !== ""),
  });
}

export type OverrideTerms = components["schemas"]["EntitlementTerms"];

function invalidateCustomer(queries: ReturnType<typeof useQueryClient>, tenantId: string, customer: string) {
  void queries.invalidateQueries({ queryKey: meterKeys.overrides(tenantId, customer) });
  void queries.invalidateQueries({ queryKey: meterKeys.entitlements(tenantId, customer) });
}

/** Creates or replaces the customer's limit on a meter. */
export function useSetOverride(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { meter: string; terms: OverrideTerms }) =>
      unwrap(
        tenantApi(toValue(tenantId)).PUT("/v1/customers/{external_id}/entitlement-overrides/{meter}", {
          params: { path: { external_id: toValue(customer), meter: v.meter } },
          body: v.terms,
        }),
      ),
    onSuccess: () => invalidateCustomer(queries, toValue(tenantId), toValue(customer)),
  });
}

/** Returns the customer to its package's limit on a meter. */
export function useDeleteOverride(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (meter: string) =>
      unwrap(
        tenantApi(toValue(tenantId)).DELETE("/v1/customers/{external_id}/entitlement-overrides/{meter}", {
          params: { path: { external_id: toValue(customer), meter } },
        }),
      ),
    onSuccess: () => invalidateCustomer(queries, toValue(tenantId), toValue(customer)),
  });
}
