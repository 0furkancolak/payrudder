import { useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";

export function useRevenueReport(tenantId: MaybeRefOrGetter<string>, from: MaybeRefOrGetter<string>, to: MaybeRefOrGetter<string>, groupBy: MaybeRefOrGetter<string[]>) {
  return useQuery({
    queryKey: computed(() => ["tenants", toValue(tenantId), "reports", "revenue", toValue(from), toValue(to), [...toValue(groupBy)].sort()] as const),
    queryFn: () =>
      unwrap(
        tenantApi(toValue(tenantId)).GET("/v1/reports/revenue", {
          params: { query: { from: toValue(from) || undefined, to: toValue(to) || undefined, group_by: toValue(groupBy).join(",") || undefined } },
        }),
      ),
  });
}

export function useSubscriptionReport(tenantId: MaybeRefOrGetter<string>, month: MaybeRefOrGetter<string>, groupBy: MaybeRefOrGetter<string[]>) {
  return useQuery({
    queryKey: computed(() => ["tenants", toValue(tenantId), "reports", "subscriptions", toValue(month), [...toValue(groupBy)].sort()] as const),
    queryFn: () =>
      unwrap(
        tenantApi(toValue(tenantId)).GET("/v1/reports/subscriptions", {
          params: { query: { month: toValue(month) || undefined, group_by: toValue(groupBy).join(",") || undefined } },
        }),
      ),
  });
}

export function useDunningReport(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => ["tenants", toValue(tenantId), "reports", "dunning"] as const),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/reports/dunning", { params: { query: { limit: 200 } } })),
  });
}
