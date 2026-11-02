import type { ApiLocale } from "@/shared/lib/locale";
import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";
import type { components } from "@/shared/api/schema";
import { restOfPages } from "@/shared/lib/bulk";
import type { Address } from "@/shared/lib/address";
import { usePayments } from "@/features/product/billingApi";
import { useSubscriptions } from "@/features/subscriptions/api";
import { summarise, type BlockCategory } from "./lib";

export const customerKeys = {
  list: (tenantId: string) => ["tenants", tenantId, "customers"] as const,
};

export function useCustomers(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => customerKeys.list(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/customers", { params: { query: { page_size: PAGE.whole } } })),
  });
}

/** The customers after the first page, fetched by page token into the list's cache: "select all N results" must cover every customer the filters match, not the first page only. */
export function useRestOfCustomers(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const key = customerKeys.list(toValue(tenantId));
      const first = queries.getQueryData<components["schemas"]["ListCustomersResponse"]>(key);
      const rest = await restOfPages(first?.next_page_token ?? "", async (token) => {
        const page = await unwrap(tenantApi(toValue(tenantId)).GET("/v1/customers", { params: { query: { page_size: PAGE.whole, page_token: token } } }));
        return { rows: page.customers ?? [], next: page.next_page_token ?? "" };
      });
      queries.setQueryData(key, { customers: [...(first?.customers ?? []), ...rest], next_page_token: "" });
    },
  });
}

/** One customer by external id (a mention in another sheet names it; the list holds only the first page of customers). */
export function useCustomerById(tenantId: MaybeRefOrGetter<string>, externalId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => [...customerKeys.list(toValue(tenantId)), "one", toValue(externalId)] as const),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/customers/{external_id}", { params: { path: { external_id: toValue(externalId) } } })),
    enabled: computed(() => toValue(externalId) !== ""),
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
  /** The mobile number in international form (the server stores it as E.164). */
  phone?: string;
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
  /** The language of the customer's mails and pages, and whether it is a person or a business; left out, the server keeps what the customer has. */
  locale?: ApiLocale;
  customer_type?: CustomerKind;
  /** The mobile number in international form; left out, the server keeps it, an empty string clears it. */
  phone?: string;
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

export interface BlockInput {
  externalId: string;
  /** The seller's own note. */
  reason?: string;
  category?: BlockCategory;
  /** When the block ends by itself (an ISO time); absent: until it is lifted. */
  until?: string;
  /** Also suspend the customer's service: entitlement checks answer "not allowed" on every meter while the block holds. */
  suspend?: boolean;
}

/** Blocks a customer from new purchases (what exists runs on), or lifts the block. The note is the seller's own. */
export function useBlockCustomer(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const done = () => queries.invalidateQueries({ queryKey: customerKeys.list(toValue(tenantId)) });
  return {
    block: useMutation({
      mutationFn: (v: BlockInput) =>
        unwrap(
          tenantApi(toValue(tenantId)).POST("/v1/customers/{external_id}/block", {
            params: { path: { external_id: v.externalId } },
            body: { reason: v.reason ?? "", ...(v.category ? { category: v.category } : {}), ...(v.until ? { blocked_until: v.until } : {}), ...(v.suspend ? { suspend_service: true } : {}) },
          }),
        ),
      onSuccess: done,
    }),
    unblock: useMutation({
      mutationFn: (externalId: string) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/customers/{external_id}/unblock", { params: { path: { external_id: externalId } } })),
      onSuccess: done,
    }),
  };
}

/** A customer's blocks and lifts, newest first: who, when, why. */
export function useCustomerBlockEvents(tenantId: MaybeRefOrGetter<string>, externalId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => [...customerKeys.list(toValue(tenantId)), "block-events", toValue(externalId)] as const),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/customers/{external_id}/block-events", { params: { path: { external_id: toValue(externalId) } } })),
    enabled: computed(() => toValue(externalId) !== ""),
  });
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

/** The invoice a customer's open period would get if it were drafted now: an estimate that moves with every event until the period closes. */
export function useUpcomingInvoice(tenantId: MaybeRefOrGetter<string>, externalId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => [...customerKeys.list(toValue(tenantId)), "upcoming-invoice", toValue(externalId)] as const),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/customers/{external_id}/upcoming-invoice", { params: { path: { external_id: toValue(externalId) } } })),
    enabled: computed(() => toValue(externalId) !== ""),
    staleTime: 15_000,
  });
}

export interface UsageCorrection {
  customer: string;
  meter: string;
  /** The idempotency key of the event to correct, as the app sent it. */
  corrects: string;
  /** The quantity that event should have had; 0 cancels it. */
  quantity: string;
  reason: string;
  /** The correction's own idempotency key: the form keeps it across retries and renews it when the correction went through. */
  key: string;
}

/**
 * Corrects a usage event (POST /v1/events/corrections). The answer is 202 whatever the verdict, so a refused correction is turned into a `CorrectionRefused` carrying the
 * code of the result, and an accepted or repeated one resolves.
 */
export function useCorrectUsage(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: async (v: UsageCorrection) => {
      const { results } = await unwrap(
        tenantApi(toValue(tenantId)).POST("/v1/events/corrections", {
          body: { corrections: [{ idempotency_key: v.key, corrects: v.corrects, reason: v.reason, customer_external_id: v.customer, meter_code: v.meter, quantity: v.quantity }] },
        }),
      );
      const r = results[0];
      if (!r || r.status === "rejected") throw new CorrectionRefused(r?.error_code ?? "", r?.error_message ?? "");
      return r;
    },
    onSuccess: (_r, v) => {
      // The estimate of the next invoice and the usage move with a correction.
      void queries.invalidateQueries({ queryKey: [...customerKeys.list(toValue(tenantId)), "upcoming-invoice", v.customer] });
      void queries.invalidateQueries({ queryKey: ["tenants", toValue(tenantId), "usage"] });
    },
  });
}

/** A correction the service refused: `code` is the event result's error_code. */
export class CorrectionRefused extends Error {
  constructor(
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}
