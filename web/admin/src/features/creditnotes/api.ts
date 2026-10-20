import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, tenantApi, unwrap } from "@/shared/api/client";

export const creditNoteKeys = {
  list: (tenantId: string, invoiceId: string) => ["tenants", tenantId, "credit-notes", invoiceId] as const,
};

/** The tenant's credit notes, newest first; of one invoice when invoiceId is given. */
export function useCreditNotes(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string> = "") {
  return useQuery({
    queryKey: computed(() => creditNoteKeys.list(toValue(tenantId), toValue(invoiceId))),
    queryFn: () => {
      const id = toValue(invoiceId);
      return unwrap(tenantApi(toValue(tenantId)).GET("/v1/credit-notes", { params: { query: { page_size: 100, ...(id ? { invoice_id: id } : {}) } } }));
    },
  });
}

/** Issues a credit note against a paid invoice (an operator action). An empty amount credits what is left of the invoice. */
export function useCreateCreditNote(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { invoiceId: string; amountMinor: string; reason: string }) =>
      unwrap(
        api.POST("/admin/v1/invoices/{id}/credit-notes", {
          params: { path: { id: v.invoiceId } },
          body: { tenant_id: toValue(tenantId), amount_minor: v.amountMinor, reason: v.reason },
        }),
      ),
    onSuccess: () => queries.invalidateQueries({ queryKey: ["tenants", toValue(tenantId), "credit-notes"] }),
  });
}
