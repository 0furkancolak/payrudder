import type { ApiLocale } from "@/shared/lib/locale";
import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";
import type { Address } from "@/shared/lib/address";
import { usePayments } from "@/features/product/billingApi";
import { useSubscriptions } from "@/features/subscriptions/api";
import { summarise } from "./lib";

export const customerKeys = {
  list: (tenantId: string) => ["tenants", tenantId, "customers"] as const,
};

export function useCustomers(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => customerKeys.list(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/customers", { params: { query: { page_size: PAGE.whole } } })),
  });
}

/** The tenant's newest invoices through the tenant API (which an operator and a person both speak), for the figures of the customer list. */
export function useRecentInvoices(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => ["tenants", toValue(tenantId), "invoices", "recent"] as const),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/invoices", { params: { query: { page_size: PAGE.long } } })),
  });
}

/**
 * Spend, open balance, subscriptions and last payment of every customer, worked out in the page from the invoices, payments and subscriptions the
 * console loads anyway (the newest page of each). `loading` holds until all three answered; a list that failed counts as empty, so the customers still
 * show, without those figures.
 */
export function useCustomerSummaries(tenantId: MaybeRefOrGetter<string>) {
  const invoices = useRecentInvoices(tenantId);
  const payments = usePayments(tenantId);
  const subscriptions = useSubscriptions(tenantId);
  const loading = computed(() => invoices.isPending.value || payments.isPending.value || subscriptions.isPending.value);
  const byCustomer = computed(() => summarise(invoices.data.value?.invoices ?? [], payments.data.value?.payments ?? [], subscriptions.data.value?.subscriptions ?? []));
  /** Whether there are older invoices than the ones loaded, so the figures may be short of the whole history. */
  const partial = computed(() => !!invoices.data.value?.next_page_token);
  return { byCustomer, loading, partial };
}

export type { Address };

/** The address as one line for a table: "Atatürk Cd. 1, 34000 İstanbul, TR". */
export function addressLine(a?: Address | null): string {
  if (!a) return "";
  return [a.line1, a.line2, [a.postal_code, a.city].filter(Boolean).join(" "), a.region, a.country].filter(Boolean).join(", ");
}

export type CustomerKind = "individual" | "business";

export interface NewCustomer {
  external_id: string;
  name: string;
  email: string;
  country?: string;
  billing_currency?: string;
  address?: Address;
  locale?: ApiLocale;
  customer_type?: CustomerKind;
}

export function useCreateCustomer(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: NewCustomer) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/customers", { body })),
    onSuccess: () => queries.invalidateQueries({ queryKey: customerKeys.list(toValue(tenantId)) }),
  });
}

/** Replaces a customer's billing address; an empty one clears it. */
export function useSetCustomerAddress(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { externalId: string; address: Address }) =>
      unwrap(tenantApi(toValue(tenantId)).PUT("/v1/customers/{external_id}/address", { params: { path: { external_id: v.externalId } }, body: v.address })),
    onSuccess: () => queries.invalidateQueries({ queryKey: customerKeys.list(toValue(tenantId)) }),
  });
}

export interface CustomerContact {
  name: string;
  email: string;
  /** The language of the customer's mails and pages, and whether it is a person or a business; empty clears them. */
  locale?: ApiLocale;
  customer_type?: CustomerKind;
}

/** Replaces a customer's name and e-mail address. */
export function useSetCustomerContact(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { externalId: string; contact: CustomerContact }) =>
      unwrap(tenantApi(toValue(tenantId)).PUT("/v1/customers/{external_id}/contact", { params: { path: { external_id: v.externalId } }, body: v.contact })),
    onSuccess: () => queries.invalidateQueries({ queryKey: customerKeys.list(toValue(tenantId)) }),
  });
}

/** Blocks a customer from new subscriptions (what exists runs on), or lifts the block. The note is the seller's own. */
export function useBlockCustomer(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const done = () => queries.invalidateQueries({ queryKey: customerKeys.list(toValue(tenantId)) });
  return {
    block: useMutation({
      mutationFn: (v: { externalId: string; reason?: string }) =>
        unwrap(tenantApi(toValue(tenantId)).POST("/v1/customers/{external_id}/block", { params: { path: { external_id: v.externalId } }, body: { reason: v.reason ?? "" } })),
      onSuccess: done,
    }),
    unblock: useMutation({
      mutationFn: (externalId: string) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/customers/{external_id}/unblock", { params: { path: { external_id: externalId } } })),
      onSuccess: done,
    }),
  };
}

export interface CustomerTax {
  tax_id?: string;
  tax_office?: string;
  e_invoice_registered?: boolean;
}

/** Replaces a customer's tax data used on e-invoices; empty values clear them. */
export function useSetCustomerTax(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { externalId: string; tax: CustomerTax }) =>
      unwrap(tenantApi(toValue(tenantId)).PUT("/v1/customers/{external_id}/tax", { params: { path: { external_id: v.externalId } }, body: v.tax })),
    onSuccess: () => queries.invalidateQueries({ queryKey: customerKeys.list(toValue(tenantId)) }),
  });
}
