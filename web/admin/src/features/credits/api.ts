import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";

export const creditKeys = {
  packs: (tenantId: string) => ["tenants", tenantId, "credit-packs"] as const,
  customer: (tenantId: string, customer: string) => ["tenants", tenantId, "credits", customer] as const,
};

export function useCreditPacks(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => creditKeys.packs(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/credit-packs", { params: { query: {} } })),
  });
}

export interface NewPack {
  name: string;
  credits: string;
  price_minor: string;
  currency: string;
}

export function useCreateCreditPack(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: NewPack) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/credit-packs", { body })),
    onSuccess: () => queries.invalidateQueries({ queryKey: creditKeys.packs(toValue(tenantId)) }),
  });
}

/** Withdraws a pack from sale or puts it back; credits already sold stay. */
export function useSetCreditPackActive(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; active: boolean }) => {
      const api = tenantApi(toValue(tenantId));
      const params = { params: { path: { id: v.id } } };
      return unwrap(v.active ? api.POST("/v1/credit-packs/{id}/activate", params) : api.POST("/v1/credit-packs/{id}/deactivate", params));
    },
    onSuccess: () => queries.invalidateQueries({ queryKey: creditKeys.packs(toValue(tenantId)) }),
  });
}

/** What one customer can spend now, and the lots it comes from. */
export function useCreditBalance(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => [...creditKeys.customer(toValue(tenantId), toValue(customer)), "balance"]),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/customers/{external_id}/credits", { params: { path: { external_id: toValue(customer) } } })),
    enabled: computed(() => toValue(customer) !== ""),
  });
}

export function useCreditLedger(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => [...creditKeys.customer(toValue(tenantId), toValue(customer)), "ledger"]),
    queryFn: () =>
      unwrap(
        tenantApi(toValue(tenantId)).GET("/v1/customers/{external_id}/credits/ledger", {
          params: { path: { external_id: toValue(customer) }, query: { page_size: 50 } },
        }),
      ),
    enabled: computed(() => toValue(customer) !== ""),
  });
}

export interface Grant {
  customer: string;
  credits: string;
  source: "promo" | "manual";
  expireTime?: string;
  reason?: string;
}

/** Adds credits to a customer. The key is made here so a double click, or a retried request, adds them once. */
export function useGrantCredits(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: Grant) =>
      unwrap(
        tenantApi(toValue(tenantId)).POST("/v1/customers/{external_id}/credits/grant", {
          params: { path: { external_id: v.customer } },
          body: { credits: v.credits, source: v.source, expire_time: v.expireTime, reason: v.reason, idempotency_key: crypto.randomUUID() },
        }),
      ),
    onSuccess: (_data, v) => queries.invalidateQueries({ queryKey: creditKeys.customer(toValue(tenantId), v.customer) }),
  });
}
