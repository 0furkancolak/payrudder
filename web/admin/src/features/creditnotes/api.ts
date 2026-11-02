import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";

export const creditNoteKeys = {
  all: (tenantId: string) => ["tenants", tenantId, "credit-notes"] as const,
  list: (tenantId: string, invoiceId: string) => ["tenants", tenantId, "credit-notes", invoiceId] as const,
};

/** The tenant's credit notes, newest first; of one invoice when invoiceId is given. */
export function useCreditNotes(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string> = "") {
  return useQuery({
    queryKey: computed(() => creditNoteKeys.list(toValue(tenantId), toValue(invoiceId))),
    queryFn: () => {
      const id = toValue(invoiceId);
      return unwrap(tenantApi(toValue(tenantId)).GET("/v1/credit-notes", { params: { query: { page_size: PAGE.long, ...(id ? { invoice_id: id } : {}) } } }));
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
    onSuccess: () => queries.invalidateQueries({ queryKey: creditNoteKeys.all(toValue(tenantId)) }),
  });
}

/** Asks the payment provider again for a credit note refund that failed (an operator action). */
export function useRetryCreditNoteRefund(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (creditNoteId: string) =>
      unwrap(api.POST("/admin/v1/credit-notes/{id}/retry-refund", { params: { path: { id: creditNoteId } }, body: { tenant_id: toValue(tenantId) } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: creditNoteKeys.all(toValue(tenantId)) }),
  });
}

/**
 * Settles a refund that failed with what the operator found out at the provider: it was made (its reference is recorded) or it was not (it is asked again under
 * a new key). The way out of an unknown outcome, which is never asked of the provider again under the same key.
 */
export function useResolveCreditNoteRefund(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { creditNoteId: string; refunded: boolean; providerRefundId?: string; note?: string }) =>
      unwrap(
        api.POST("/admin/v1/credit-notes/{id}/resolve-refund", {
          params: { path: { id: v.creditNoteId } },
          body: {
            tenant_id: toValue(tenantId),
            resolution: v.refunded ? "REFUND_RESOLUTION_REFUNDED" : "REFUND_RESOLUTION_NOT_REFUNDED",
            ...(v.refunded ? { provider_refund_id: v.providerRefundId } : {}),
            ...(v.note ? { note: v.note } : {}),
          },
        }),
      ),
    onSuccess: () => queries.invalidateQueries({ queryKey: creditNoteKeys.all(toValue(tenantId)) }),
  });
}
