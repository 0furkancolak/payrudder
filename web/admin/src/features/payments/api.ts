import { useMutation, useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";

export const paymentKeys = {
  ledger: (tenantId: string) => ["tenants", tenantId, "ledger"] as const,
};

/** The tenant's payment ledger, newest first. */
export function useLedger(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => paymentKeys.ledger(toValue(tenantId))),
    queryFn: () => unwrap(api.GET("/admin/v1/tenants/{tenant_id}/ledger", { params: { path: { tenant_id: toValue(tenantId) }, query: { limit: 200 } } })),
  });
}

/** Compares a period's paid invoices with the ledger. */
export function useReconcile(tenantId: MaybeRefOrGetter<string>) {
  return useMutation({
    mutationFn: (period: string) =>
      unwrap(api.GET("/admin/v1/tenants/{tenant_id}/reconciliation", { params: { path: { tenant_id: toValue(tenantId) }, query: { period } } })),
  });
}
