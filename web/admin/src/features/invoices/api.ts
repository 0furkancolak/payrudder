import { useMutation, useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, ApiError, tenantApi, unwrap } from "@/shared/api/client";
import { isPerson } from "@/shared/lib/session";

export const invoiceKeys = {
  list: (tenantId: string) => ["tenants", tenantId, "invoices"] as const,
  detail: (tenantId: string, invoiceId: string) => ["tenants", tenantId, "invoices", invoiceId] as const,
  saga: (tenantId: string, invoiceId: string) => ["tenants", tenantId, "invoices", invoiceId, "saga"] as const,
  transfer: (tenantId: string, invoiceId: string) => ["tenants", tenantId, "invoices", invoiceId, "transfer"] as const,
};

/**
 * The invoices of a tenant. A person has no admin API; the tenant API (`/v1/invoices`, with the tenant named) answers the same rows, so the same pages
 * serve both. Operator-only parts of an invoice page (the saga, recording payments, credit notes) are not shown to a person.
 */
export function useTenantInvoices(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => [...invoiceKeys.list(toValue(tenantId)), isPerson.value ? "person" : "operator"]),
    queryFn: () =>
      isPerson.value
        ? unwrap(tenantApi(toValue(tenantId)).GET("/v1/invoices", { params: { query: { page_size: 100 } } }))
        : unwrap(api.GET("/admin/v1/tenants/{tenant_id}/invoices", { params: { path: { tenant_id: toValue(tenantId) }, query: { page_size: 100 } } })),
  });
}

export function useInvoice(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => [...invoiceKeys.detail(toValue(tenantId), toValue(invoiceId)), isPerson.value ? "person" : "operator"]),
    queryFn: () =>
      isPerson.value
        ? unwrap(tenantApi(toValue(tenantId)).GET("/v1/invoices/{id}", { params: { path: { id: toValue(invoiceId) } } }))
        : unwrap(api.GET("/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}", { params: { path: { tenant_id: toValue(tenantId), invoice_id: toValue(invoiceId) } } })),
  });
}

export function useInvoiceSaga(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => invoiceKeys.saga(toValue(tenantId), toValue(invoiceId))),
    queryFn: () =>
      unwrap(api.GET("/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/saga", { params: { path: { tenant_id: toValue(tenantId), invoice_id: toValue(invoiceId) } } })),
    // The saga is an operator's view; a person's session has no admin API.
    enabled: computed(() => !isPerson.value),
  });
}

/** Asks for a presigned link (valid for five minutes) and opens the PDF in a new tab. */
export function useOpenPdf(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string>) {
  return useMutation({
    mutationFn: async () => {
      const r = await unwrap(
        isPerson.value
          ? tenantApi(toValue(tenantId)).GET("/v1/invoices/{id}/pdf", { params: { path: { id: toValue(invoiceId) } } })
          : api.GET("/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/pdf", { params: { path: { tenant_id: toValue(tenantId), invoice_id: toValue(invoiceId) } } }),
      );
      if (!r.url) throw new ApiError("no link", "NO_LINK");
      return r.url;
    },
    onSuccess: (url) => {
      window.open(url, "_blank", "noopener");
    },
  });
}

/** Makes (or revokes) the links with which the invoice's customer can open it without signing in. */
export function useInvoiceLinks(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string>) {
  const path = () => ({ params: { path: { id: toValue(invoiceId) } } });
  return {
    create: useMutation({
      mutationFn: (days: number) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/invoices/{id}/links", { ...path(), body: { expires_in_days: days } })),
    }),
    revoke: useMutation({ mutationFn: () => unwrap(tenantApi(toValue(tenantId)).DELETE("/v1/invoices/{id}/links", path())) }),
  };
}
