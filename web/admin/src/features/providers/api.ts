import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, tenantApi, unwrap } from "@/shared/api/client";

export const providerKeys = {
  accounts: (tenantId: string) => ["tenants", tenantId, "provider-accounts"] as const,
  rules: (tenantId: string) => ["tenants", tenantId, "routing-rules"] as const,
  subscriptions: (tenantId: string) => ["tenants", tenantId, "provider-subscriptions"] as const,
};

type Env = "sandbox" | "live";

export function useProviderAccounts(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => providerKeys.accounts(toValue(tenantId))),
    queryFn: () => unwrap(api.GET("/admin/v1/tenants/{tenant_id}/provider-accounts", { params: { path: { tenant_id: toValue(tenantId) } } })),
  });
}

export function useAccountActions(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const refresh = () => queries.invalidateQueries({ queryKey: providerKeys.accounts(toValue(tenantId)) });
  return {
    save: useMutation({
      mutationFn: (v: { provider: string; environment: Env; makeDefault: boolean; settings: Record<string, unknown> }) =>
        unwrap(
          api.PUT("/admin/v1/tenants/{tenant_id}/provider-accounts/{provider}", {
            params: { path: { tenant_id: toValue(tenantId), provider: v.provider }, query: { environment: v.environment, default: v.makeDefault } },
            body: v.settings as never,
          }),
        ),
      onSuccess: refresh,
    }),
    remove: useMutation({
      mutationFn: (v: { provider: string; environment: Env }) =>
        unwrap(
          api.DELETE("/admin/v1/tenants/{tenant_id}/provider-accounts/{provider}", {
            params: { path: { tenant_id: toValue(tenantId), provider: v.provider }, query: { environment: v.environment } },
          }),
        ),
      onSuccess: refresh,
    }),
  };
}

export interface Rule {
  country: string;
  currency: string;
  provider: string;
}

export function useRoutingRules(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => providerKeys.rules(toValue(tenantId))),
    queryFn: async (): Promise<Rule[]> => {
      const r = await unwrap(tenantApi(toValue(tenantId)).GET("/v1/routing-rules"));
      return (r.rules ?? []).map((x) => ({ country: x.country ?? "", currency: x.currency ?? "", provider: x.provider ?? "" }));
    },
  });
}

export function useSetRoutingRules(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (rules: Rule[]) =>
      unwrap(
        tenantApi(toValue(tenantId)).PUT("/v1/routing-rules", {
          body: { rules: rules.map((r) => ({ country: r.country, currency: r.currency, provider: r.provider as "iyzico" | "paddle" })) },
        }),
      ),
    onSuccess: () => queries.invalidateQueries({ queryKey: providerKeys.rules(toValue(tenantId)) }),
  });
}

/** Where a customer in a country paying in a currency would go. */
export function usePreviewRouting(tenantId: MaybeRefOrGetter<string>) {
  return useMutation({
    mutationFn: (v: { country: string; currency: string }) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/routing-rules/preview", { body: v })),
  });
}

export function useProviderSubscriptions(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => providerKeys.subscriptions(toValue(tenantId))),
    queryFn: () => unwrap(api.GET("/admin/v1/tenants/{tenant_id}/provider-subscriptions", { params: { path: { tenant_id: toValue(tenantId) } } })),
  });
}
