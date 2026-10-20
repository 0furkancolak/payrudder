import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, tenantApi, unwrap } from "@/shared/api/client";

export const subscriptionKeys = {
  list: (tenantId: string) => ["tenants", tenantId, "subscriptions"] as const,
};

export function useSubscriptions(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => subscriptionKeys.list(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/subscriptions", { params: { query: { page_size: 100 } } })),
  });
}

export interface NewSubscription {
  customer_external_id: string;
  plan_code: string;
  currency?: string;
  trial_days?: number;
  provider?: string;
  interval?: "SUBSCRIPTION_INTERVAL_MONTH" | "SUBSCRIPTION_INTERVAL_YEAR";
}

export interface ChangeInput {
  id: string;
  /** Empty keeps the package. */
  plan_code: string;
  /** Empty keeps the interval. */
  interval: "" | "SUBSCRIPTION_INTERVAL_MONTH" | "SUBSCRIPTION_INTERVAL_YEAR";
  /** Empty lets Quotify decide by the direction of the change. */
  timing: "" | "CHANGE_TIMING_IMMEDIATELY" | "CHANGE_TIMING_PERIOD_END";
  proration: "" | "CHANGE_PRORATION_PRORATE" | "CHANGE_PRORATION_FULL" | "CHANGE_PRORATION_NONE";
}

/** Only what was chosen is sent: the rest is the server's default for the direction of the change. */
function changeBody(v: ChangeInput) {
  return {
    ...(v.plan_code ? { plan_code: v.plan_code } : {}),
    ...(v.interval ? { interval: v.interval } : {}),
    ...(v.timing ? { timing: v.timing } : {}),
    ...(v.proration ? { proration: v.proration } : {}),
  };
}

/** Every action on a subscription: each refreshes the list. */
export function useSubscriptionActions(tenantId: MaybeRefOrGetter<string>, environment: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const refresh = () => queries.invalidateQueries({ queryKey: subscriptionKeys.list(toValue(tenantId)) });
  const t = () => tenantApi(toValue(tenantId));
  const path = (id: string) => ({ params: { path: { id } } });
  return {
    create: useMutation({ mutationFn: (body: NewSubscription) => unwrap(t().POST("/v1/subscriptions", { body })), onSuccess: refresh }),
    pause: useMutation({ mutationFn: (id: string) => unwrap(t().POST("/v1/subscriptions/{id}/pause", path(id))), onSuccess: refresh }),
    resume: useMutation({ mutationFn: (id: string) => unwrap(t().POST("/v1/subscriptions/{id}/resume", path(id))), onSuccess: refresh }),
    scheduleCancel: useMutation({ mutationFn: (id: string) => unwrap(t().POST("/v1/subscriptions/{id}/cancel-at-period-end", path(id))), onSuccess: refresh }),
    withdrawCancel: useMutation({ mutationFn: (id: string) => unwrap(t().DELETE("/v1/subscriptions/{id}/cancel-at-period-end", path(id))), onSuccess: refresh }),
    endNow: useMutation({ mutationFn: (id: string) => unwrap(t().DELETE("/v1/subscriptions/{id}", path(id))), onSuccess: refresh }),
    /** Prices a change of plan or interval without making it. */
    previewChange: useMutation({ mutationFn: (v: ChangeInput) => unwrap(t().POST("/v1/subscriptions/{id}/change-preview", { ...path(v.id), body: changeBody(v) })) }),
    change: useMutation({ mutationFn: (v: ChangeInput) => unwrap(t().POST("/v1/subscriptions/{id}/change", { ...path(v.id), body: changeBody(v) })), onSuccess: refresh }),
    cancelScheduledChange: useMutation({ mutationFn: (id: string) => unwrap(t().DELETE("/v1/subscriptions/{id}/scheduled-change", path(id))), onSuccess: refresh }),
    changePlan: useMutation({
      mutationFn: (v: { id: string; plan_code: string }) => unwrap(t().POST("/v1/subscriptions/{id}/change-plan", { ...path(v.id), body: { plan_code: v.plan_code } })),
      onSuccess: refresh,
    }),
    /** Moves a subscription to another payment provider (an operator action: it goes through the provider accounts, not the tenant API). */
    migrate: useMutation({
      mutationFn: (v: { subscription_id: string; to_provider: string; price_ref?: string }) =>
        unwrap(
          api.POST("/admin/v1/tenants/{tenant_id}/provider-migrations", {
            params: { path: { tenant_id: toValue(tenantId) }, query: { environment: toValue(environment) as "sandbox" | "live" } },
            body: { ...v, to_provider: v.to_provider as "iyzico" | "paddle" },
          }),
        ),
      onSuccess: refresh,
    }),
  };
}

/** A customer's usage against the limits of its package, this period: the numbers the product shows next to a subscription. */
export function useEntitlements(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => ["tenants", toValue(tenantId), "entitlements", toValue(customer)] as const),
    enabled: computed(() => toValue(customer) !== ""),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/customers/{external_id}/entitlements", { params: { path: { external_id: toValue(customer) } } })),
  });
}
