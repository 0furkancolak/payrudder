import { useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";

export const reportKeys = {
  revenue: (tenantId: string, from: string, to: string, groupBy: string[]) => ["tenants", tenantId, "reports", "revenue", from, to, [...groupBy].sort()] as const,
  subscriptions: (tenantId: string, month: string, groupBy: string[]) => ["tenants", tenantId, "reports", "subscriptions", month, [...groupBy].sort()] as const,
  dunning: (tenantId: string) => ["tenants", tenantId, "reports", "dunning"] as const,
};

export function useRevenueReport(tenantId: MaybeRefOrGetter<string>, from: MaybeRefOrGetter<string>, to: MaybeRefOrGetter<string>, groupBy: MaybeRefOrGetter<string[]>) {
  return useQuery({
    queryKey: computed(() => reportKeys.revenue(toValue(tenantId), toValue(from), toValue(to), toValue(groupBy))),
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
    queryKey: computed(() => reportKeys.subscriptions(toValue(tenantId), toValue(month), toValue(groupBy))),
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
    queryKey: computed(() => reportKeys.dunning(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/reports/dunning", { params: { query: { limit: PAGE.whole } } })),
  });
}
