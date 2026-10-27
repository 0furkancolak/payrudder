import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap, type Tenant } from "@/shared/api/client";
import { PAGE, POLL } from "@/shared/api/limits";
import { usePresignedOpen } from "@/shared/api/presigned";
import type { components } from "@/shared/api/schema";
import { tenantKeys } from "@/features/tenants/api";
import type { Environment } from "@/shared/lib/environment";

export type EInvoiceDocument = components["schemas"]["EInvoiceDocument"];
export type EInvoiceAccount = components["schemas"]["EInvoiceAccount"];
export type LegalInfo = components["schemas"]["LegalInfo"];
export type EInvoiceOn = "off" | "finalized" | "paid";

export const einvoiceKeys = {
  documents: (tenantId: string) => ["tenants", tenantId, "e-invoices"] as const,
  forInvoice: (tenantId: string, invoiceId: string) => ["tenants", tenantId, "e-invoices", "invoice", invoiceId] as const,
  forCreditNote: (tenantId: string, creditNoteId: string) => ["tenants", tenantId, "e-invoices", "credit-note", creditNoteId] as const,
  accounts: (tenantId: string) => ["tenants", tenantId, "e-invoice-accounts"] as const,
};

/** The e-documents of an invoice (kind "invoice") or of a credit note; a pending one is polled until the worker answers. */
export function useEInvoiceDocuments(tenantId: MaybeRefOrGetter<string>, source: MaybeRefOrGetter<{ invoiceId?: string; creditNoteId?: string }>) {
  return useQuery({
    queryKey: computed(() => {
      const s = toValue(source);
      return s.creditNoteId ? einvoiceKeys.forCreditNote(toValue(tenantId), s.creditNoteId) : einvoiceKeys.forInvoice(toValue(tenantId), s.invoiceId ?? "");
    }),
    queryFn: async () => {
      const s = toValue(source);
      const r = await unwrap(
        api.GET("/admin/v1/tenants/{tenant_id}/e-invoices", {
          params: { path: { tenant_id: toValue(tenantId) }, query: s.creditNoteId ? { credit_note_id: s.creditNoteId, page_size: PAGE.few } : { invoice_id: s.invoiceId, page_size: PAGE.few } },
        }),
      );
      const wanted = s.creditNoteId ? "credit_note" : "invoice";
      return (r.documents ?? []).filter((d) => d.kind === wanted);
    },
    refetchInterval: (q) => ((q.state.data ?? []).some((d) => d.status === "pending") ? POLL.pending : false),
  });
}

/** Retry (failed), cancel (issued) and download are operator actions. */
export function useEInvoiceActions(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const done = () => queries.invalidateQueries({ queryKey: einvoiceKeys.documents(toValue(tenantId)) });
  const path = (id: string) => ({ params: { path: { tenant_id: toValue(tenantId), id } } });
  return {
    retry: useMutation({ mutationFn: (id: string) => unwrap(api.POST("/admin/v1/tenants/{tenant_id}/e-invoices/{id}/retry", path(id))), onSuccess: done }),
    cancel: useMutation({ mutationFn: (id: string) => unwrap(api.POST("/admin/v1/tenants/{tenant_id}/e-invoices/{id}/cancel", path(id))), onSuccess: done }),
    /** Opens the XML or PDF of a document in a new tab. */
    download: usePresignedOpen((v: { id: string; kind: "xml" | "pdf" }) =>
      unwrap(api.GET("/admin/v1/tenants/{tenant_id}/e-invoices/{id}/files/{kind}", { params: { path: { tenant_id: toValue(tenantId), id: v.id, kind: v.kind } } })),
    ),
  };
}

export function useEInvoiceAccounts(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => einvoiceKeys.accounts(toValue(tenantId))),
    queryFn: async () => (await unwrap(api.GET("/admin/v1/tenants/{tenant_id}/e-invoice-accounts", { params: { path: { tenant_id: toValue(tenantId) } } }))).accounts ?? [],
  });
}

export function useEInvoiceAccountActions(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const done = () => queries.invalidateQueries({ queryKey: einvoiceKeys.accounts(toValue(tenantId)) });
  return {
    save: useMutation({
      mutationFn: (v: { environment: Environment; provider: "mock" | "nilvera" | "webhook"; mode: "test" | "production"; credentials: Record<string, string> }) =>
        unwrap(
          api.PUT("/admin/v1/tenants/{tenant_id}/e-invoice-accounts/{environment}", {
            params: { path: { tenant_id: toValue(tenantId), environment: v.environment } },
            body: { provider: v.provider, mode: v.mode, credentials: v.credentials },
          }),
        ),
      onSuccess: done,
    }),
    remove: useMutation({
      mutationFn: (environment: Environment) =>
        unwrap(api.DELETE("/admin/v1/tenants/{tenant_id}/e-invoice-accounts/{environment}", { params: { path: { tenant_id: toValue(tenantId), environment } } })),
      onSuccess: done,
    }),
  };
}

/** Saves the setting and the company legal data with the tenant PATCH. */
export function useSaveEInvoiceSettings(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { legal: LegalInfo; eInvoiceOn: EInvoiceOn }) =>
      unwrap(api.PATCH("/admin/v1/tenants/{id}", { params: { path: { id: toValue(tenantId) } }, body: { legal: v.legal, e_invoice_on: v.eInvoiceOn } })),
    onSuccess: (t: Tenant) => {
      queries.setQueryData(tenantKeys.detail(toValue(tenantId)), t);
      return queries.invalidateQueries({ queryKey: tenantKeys.detail(toValue(tenantId)) });
    },
  });
}
