import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { invoiceKeys } from "./api";

// What an invoice's payment waits for, and the seller's two answers to a held first charge. Like the disputes beside it these speak the tenant API (/v1/*) as the tenant:
// the person's session plus X-Payrudder-Tenant, or the operator acting as the tenant.

export const waitKeys = {
  wait: (tenantId: string, invoiceId: string) => ["app", "tenant", tenantId, "invoice", invoiceId, "payment-wait"] as const,
};

/** What the invoice's payment waits for; asked only while `enabled` (the saga is in a state that waits). */
export function useInvoicePaymentWait(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string>, enabled: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: computed(() => waitKeys.wait(toValue(tenantId), toValue(invoiceId))),
    enabled: computed(() => toValue(enabled)),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/invoices/{id}/payment-wait", { params: { path: { id: toValue(invoiceId) } } })),
  });
}

/** Release (ask the payment gate again, charge if it lets the invoice through) and void (end the invoice, nothing was charged) a held first charge. Each refreshes everything of the tenant. */
export function useHeldInvoiceActions(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const path = () => ({ params: { path: { id: toValue(invoiceId) } } });
  const refresh = async () => {
    await Promise.all([queries.invalidateQueries({ queryKey: ["app", "tenant", toValue(tenantId)] }), queries.invalidateQueries({ queryKey: invoiceKeys.list(toValue(tenantId)) })]);
  };
  return {
    release: useMutation({ mutationFn: () => unwrap(tenantApi(toValue(tenantId)).POST("/v1/invoices/{id}/release", path())), onSuccess: refresh }),
    voidIt: useMutation({ mutationFn: () => unwrap(tenantApi(toValue(tenantId)).POST("/v1/invoices/{id}/void", path())), onSuccess: refresh }),
  };
}
