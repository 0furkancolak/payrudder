import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";

export const creditKeys = {
  packs: (tenantId: string) => ["tenants", tenantId, "credit-packs"] as const,
  settings: (tenantId: string) => ["tenants", tenantId, "credit-settings"] as const,
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
  /** The credits bought last this many days; leave out for credits that never expire. */
  validity_days?: number;
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
          params: { path: { external_id: toValue(customer) }, query: { page_size: PAGE.history } },
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
  /** The idempotency key of this attempt: the form keeps it across retries and renews it when the grant went through. */
  key: string;
}

/** Adds credits to a customer. The caller's key (one per attempt, see useAttemptKey) makes a double click or a retried request add them once. */
export function useGrantCredits(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: Grant) =>
      unwrap(
        tenantApi(toValue(tenantId)).POST("/v1/customers/{external_id}/credits/grant", {
          params: { path: { external_id: v.customer } },
          body: { credits: v.credits, source: v.source, expire_time: v.expireTime, reason: v.reason, idempotency_key: v.key },
        }),
      ),
    onSuccess: (_data, v) => queries.invalidateQueries({ queryKey: creditKeys.customer(toValue(tenantId), v.customer) }),
  });
}

/** The balance below which a consume sends the credits.low webhook event (0: off). */
export function useCreditSettings(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => creditKeys.settings(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/credits/settings")),
  });
}

export function useSetCreditSettings(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (lowThreshold: string) => unwrap(tenantApi(toValue(tenantId)).PUT("/v1/credits/settings", { body: { low_threshold: lowThreshold } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: creditKeys.settings(toValue(tenantId)) }),
  });
}

export interface PurchaseLink {
  packId: string;
  customer: string;
  quantity: number;
  /** A promotion code the customer gets without typing it (optional). */
  promotion?: string;
}

/**
 * Makes the link a customer opens to buy a pack. The price comes from the pack, so the request carries none. The URL is in the response once and is not stored: the
 * key is made per submission, so a retry of the same click finds the link made first (without its address).
 */
export function useCreatePurchaseLink(tenantId: MaybeRefOrGetter<string>) {
  return useMutation({
    mutationFn: (v: PurchaseLink & { key: string }) =>
      unwrap(
        tenantApi(toValue(tenantId)).POST("/v1/payment-links", {
          body: { kind: "credit_pack", pack_id: v.packId, customer_external_id: v.customer, quantity: v.quantity, idempotency_key: v.key, ...(v.promotion?.trim() ? { promotion_code: v.promotion.trim() } : {}) },
        }),
      ),
  });
}
