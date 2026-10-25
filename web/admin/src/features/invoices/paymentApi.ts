import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, ApiError, unwrap } from "@/shared/api/client";
import { invoiceKeys } from "./api";
import { ACTOR, type InvoiceTransfer, type PaymentMethod } from "./payment";

/**
 * The bank transfer state of an invoice. "Not found" and "service unavailable" are "no transfer information", not errors: the invoice page must work
 * without it, so the query resolves to null for those two. Any other failure (a refused session, a bug) throws and shows.
 */
export function useInvoiceTransfer(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string>, enabled: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: computed(() => invoiceKeys.transfer(toValue(tenantId), toValue(invoiceId))),
    enabled: computed(() => toValue(enabled)),
    retry: false,
    queryFn: async (): Promise<InvoiceTransfer | null> => {
      try {
        return await unwrap(
          api.GET("/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/transfer", { params: { path: { tenant_id: toValue(tenantId), invoice_id: toValue(invoiceId) } } }),
        );
      } catch (e) {
        // No transfer on this invoice, or the payments service is down: the page works without it. A refusal (401, 403) or anything else is a real failure.
        if (e instanceof ApiError && (e.code === "NOT_FOUND" || e.code === "UNAVAILABLE")) return null;
        throw e;
      }
    },
  });
}

export interface ConfirmTransferInput {
  amountMinor: number;
  currency: string;
  receivedAt: string;
  bankReference: string;
  note: string;
}

export interface RecordPaymentInput {
  method: PaymentMethod;
  amountMinor: string;
  receivedAt: string;
  reference: string;
  note: string;
}

/**
 * The operator's actions on an invoice's money: confirm a transfer, record an offline payment, give up on a pending transfer. Each refreshes the tenant's
 * invoices, which covers the invoice, its saga and its transfer state (their query keys start with the list's).
 */
export function useInvoicePayments(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const refresh = () => queries.invalidateQueries({ queryKey: invoiceKeys.list(toValue(tenantId)) });
  const header = { "X-Quotify-Actor": ACTOR };
  return {
    confirmTransfer: useMutation({
      mutationFn: (v: ConfirmTransferInput) =>
        unwrap(
          api.POST("/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/transfer/receipts", {
            params: { path: { tenant_id: toValue(tenantId), invoice_id: toValue(invoiceId) }, header },
            body: { amount_minor: v.amountMinor, currency: v.currency, received_at: v.receivedAt, bank_reference: v.bankReference, ...(v.note ? { note: v.note } : {}) },
          }),
        ),
      onSuccess: refresh,
    }),
    recordPayment: useMutation({
      mutationFn: (v: RecordPaymentInput) =>
        unwrap(
          api.POST("/admin/v1/invoices/{id}/payments", {
            params: { path: { id: toValue(invoiceId) }, header },
            body: {
              tenant_id: toValue(tenantId),
              method: v.method,
              amount_minor: v.amountMinor,
              received_at: v.receivedAt,
              ...(v.reference ? { reference: v.reference } : {}),
              ...(v.note ? { note: v.note } : {}),
            },
          }),
        ),
      onSuccess: refresh,
    }),
    rejectTransfer: useMutation({
      mutationFn: (v: { outcome: "rejected" | "expired"; reason: string }) =>
        unwrap(
          api.POST("/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/transfer/reject", {
            params: { path: { tenant_id: toValue(tenantId), invoice_id: toValue(invoiceId) }, header },
            body: { outcome: v.outcome, ...(v.reason ? { reason: v.reason } : {}) },
          }),
        ),
      onSuccess: refresh,
    }),
  };
}
