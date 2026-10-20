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
    mutationFn: (v: { invoiceId: string; amountMinor: string; reason: string; refundToCard?: boolean }) =>
      unwrap(
        api.POST("/admin/v1/invoices/{id}/credit-notes", {
          params: { path: { id: v.invoiceId } },
          body: { tenant_id: toValue(tenantId), amount_minor: v.amountMinor, reason: v.reason, ...(v.refundToCard ? { refund_to_card: true } : {}) },
        }),
      ),
    onSuccess: () => queries.invalidateQueries({ queryKey: ["tenants", toValue(tenantId), "credit-notes"] }),
  });
}

/** Asks the payment provider again for a credit note refund that failed (an operator action). */
export function useRetryCreditNoteRefund(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (creditNoteId: string) =>
      unwrap(api.POST("/admin/v1/credit-notes/{id}/retry-refund", { params: { path: { id: creditNoteId } }, body: { tenant_id: toValue(tenantId) } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: ["tenants", toValue(tenantId), "credit-notes"] }),
  });
}
