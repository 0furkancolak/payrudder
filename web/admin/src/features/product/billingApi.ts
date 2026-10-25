import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { ApiError, tenantApi, unwrap } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";

// The billing pages of the product app speak the tenant API (/v1/*) as the person's tenant: the session token plus X-Payrudder-Tenant. Nothing here uses an
// /admin route. Types come from the OpenAPI file.

type S = components["schemas"];
export type Invoice = S["Invoice"];
export type Payment = S["Payment"];
export type InvoiceStatus = "draft" | "open" | "paid" | "uncollectible" | "void";

export const billingKeys = {
  invoices: (tenantId: string, status: string) => ["app", "tenant", tenantId, "invoices", status] as const,
  invoice: (tenantId: string, id: string) => ["app", "tenant", tenantId, "invoice", id] as const,
  payments: (tenantId: string) => ["app", "tenant", tenantId, "payments"] as const,
};

const PAGE = 50;

/** The tenant's invoices, newest page first, with "load more" through the page token. An empty status means all. */
export function useInvoices(tenantId: MaybeRefOrGetter<string>, status: MaybeRefOrGetter<string>) {
  return useInfiniteQuery({
    queryKey: computed(() => billingKeys.invoices(toValue(tenantId), toValue(status))),
    initialPageParam: "",
    queryFn: ({ pageParam }) =>
      unwrap(
        tenantApi(toValue(tenantId)).GET("/v1/invoices", {
          params: { query: { page_size: PAGE, page_token: pageParam || undefined, status: (toValue(status) || undefined) as InvoiceStatus | undefined } },
        }),
      ),
    getNextPageParam: (last) => last.next_page_token || undefined,
  });
}

export function useInvoice(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => billingKeys.invoice(toValue(tenantId), toValue(invoiceId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/invoices/{id}", { params: { path: { id: toValue(invoiceId) } } })),
  });
}

/** Asks for a presigned link (valid for five minutes) and opens the PDF in a new tab. */
export function useOpenInvoicePdf(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string>) {
  return useMutation({
    mutationFn: async () => {
      const r = await unwrap(tenantApi(toValue(tenantId)).GET("/v1/invoices/{id}/pdf", { params: { path: { id: toValue(invoiceId) } } }));
      if (!r.url) throw new ApiError("no link", "NO_LINK");
      return r.url;
    },
    onSuccess: (url) => {
      window.open(url, "_blank", "noopener");
    },
  });
}

/** The tenant's payments, newest first (charges and refunds, each folded to its settled outcome). */
export function usePayments(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => billingKeys.payments(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/payments", { params: { query: { limit: 200 } } })),
  });
}
