import { useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";

export const disputeKeys = {
  list: (tenantId: string, invoiceId: string) => ["app", "tenant", tenantId, "disputes", invoiceId] as const,
};

/** The cases customers opened with their banks, newest first: the tenant's, or one invoice's when an id is given. Reads GET /v1/disputes. */
export function useDisputes(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string> = "") {
  return useQuery({
    queryKey: computed(() => disputeKeys.list(toValue(tenantId), toValue(invoiceId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/disputes", { params: { query: { limit: PAGE.whole, invoice_id: toValue(invoiceId) || undefined } } })),
  });
}
