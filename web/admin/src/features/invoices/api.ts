import { useMutation, useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, ApiError, unwrap } from "@/shared/api/client";

export const invoiceKeys = {
  list: (tenantId: string) => ["tenants", tenantId, "invoices"] as const,
  detail: (tenantId: string, invoiceId: string) => ["tenants", tenantId, "invoices", invoiceId] as const,
  saga: (tenantId: string, invoiceId: string) => ["tenants", tenantId, "invoices", invoiceId, "saga"] as const,
};

export function useTenantInvoices(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => invoiceKeys.list(toValue(tenantId))),
    queryFn: () => unwrap(api.GET("/admin/v1/tenants/{tenant_id}/invoices", { params: { path: { tenant_id: toValue(tenantId) }, query: { page_size: 100 } } })),
  });
}

export function useInvoice(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => invoiceKeys.detail(toValue(tenantId), toValue(invoiceId))),
    queryFn: () =>
      unwrap(api.GET("/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}", { params: { path: { tenant_id: toValue(tenantId), invoice_id: toValue(invoiceId) } } })),
  });
}

export function useInvoiceSaga(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => invoiceKeys.saga(toValue(tenantId), toValue(invoiceId))),
    queryFn: () =>
      unwrap(api.GET("/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/saga", { params: { path: { tenant_id: toValue(tenantId), invoice_id: toValue(invoiceId) } } })),
  });
}

/** Asks for a presigned link (valid for five minutes) and opens the PDF in a new tab. */
export function useOpenPdf(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string>) {
  return useMutation({
    mutationFn: async () => {
      const r = await unwrap(
        api.GET("/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/pdf", { params: { path: { tenant_id: toValue(tenantId), invoice_id: toValue(invoiceId) } } }),
      );
      if (!r.url) throw new ApiError("no link", "NO_LINK");
      return r.url;
    },
    onSuccess: (url) => {
      window.open(url, "_blank", "noopener");
    },
  });
}
