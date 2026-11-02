import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";
import type { components } from "@/shared/api/schema";
import { acceptanceQuery, type AcceptanceFilter, type Audience, type Kind, type SettingsDraft } from "./lib";

type Schemas = components["schemas"];

export const contractKeys = {
  versions: (tenantId: string) => ["tenants", tenantId, "contract-versions"] as const,
  settings: (tenantId: string) => ["tenants", tenantId, "contract-settings"] as const,
  withdrawals: (tenantId: string) => ["tenants", tenantId, "contract-withdrawals"] as const,
  acceptances: (tenantId: string, customer: string) => ["tenants", tenantId, "contract-acceptances", customer] as const,
  invoice: (tenantId: string, id: string) => ["tenants", tenantId, "contract-invoice", id] as const,
  paidInvoices: (tenantId: string, customer: string) => ["tenants", tenantId, "invoices", "paid-of-customer", customer] as const,
};

// No tenant yet (the page is still resolving its app): a call without X-Payrudder-Tenant would be refused with a 401.
const ready = (tenantId: MaybeRefOrGetter<string>, on: MaybeRefOrGetter<boolean> = true) => computed(() => toValue(tenantId) !== "" && toValue(on));

/** Every version of every series, newest first. */
export function useContractVersions(tenantId: MaybeRefOrGetter<string>, on?: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: computed(() => contractKeys.versions(toValue(tenantId))),
    queryFn: async () => (await unwrap(tenantApi(toValue(tenantId)).GET("/v1/contracts/versions"))).versions ?? [],
    enabled: ready(tenantId, on),
  });
}

export function useContractSettings(tenantId: MaybeRefOrGetter<string>, on?: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: computed(() => contractKeys.settings(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/contracts/settings")),
    enabled: ready(tenantId, on),
  });
}

/** Publishes the next version of a series. A first version also makes its kind requirable, so the settings are read again. */
export function usePublishVersion(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: Schemas["PublishContractVersionRequest"]) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/contracts/versions", { body })),
    onSuccess: () => Promise.all([queries.invalidateQueries({ queryKey: contractKeys.versions(toValue(tenantId)) }), queries.invalidateQueries({ queryKey: contractKeys.settings(toValue(tenantId)) })]),
  });
}

/** Renders a text the way a customer sees it (server-escaped HTML with sample values marked) and lists the variables it uses and the ones that do not exist. */
export function usePreviewContract(tenantId: MaybeRefOrGetter<string>) {
  return useMutation({ mutationFn: (v: { body: string; locale: string }) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/contracts/preview", { body: v })) });
}

/** The starter text of a kind in Turkish or English; the API answers plans.contract.no_starter for any other language. */
export function useStarterText(tenantId: MaybeRefOrGetter<string>) {
  return useMutation({
    mutationFn: (v: { kind: Kind; audience: Audience; locale: "tr" | "en" }) => unwrap(tenantApi(toValue(tenantId)).GET("/v1/contracts/starter", { params: { query: v } })),
  });
}

export function useSetContractSettings(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: SettingsDraft) => unwrap(tenantApi(toValue(tenantId)).PUT("/v1/contracts/settings", { body })),
    onSuccess: (data) => queries.setQueryData(contractKeys.settings(toValue(tenantId)), data),
  });
}

/** The withdrawal requests of the tenant, newest first, or of one customer. */
export function useContractWithdrawals(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string> = "") {
  return useQuery({
    queryKey: computed(() => [...contractKeys.withdrawals(toValue(tenantId)), toValue(customer)] as const),
    queryFn: async () => {
      const who = toValue(customer);
      const query = who === "" ? undefined : { customer_external_id: who };
      return (await unwrap(tenantApi(toValue(tenantId)).GET("/v1/contract-withdrawals", { params: { query } }))).withdrawals ?? [];
    },
    enabled: ready(tenantId),
  });
}

/**
 * Decides a withdrawal (accepted or rejected, once) or marks an accepted one refunded; the seller refunds, Payrudder does not move the money unless the settings ask for it.
 * `refundAmountMinor` (accepting only) is what to refund when it is less than what is left of the invoice; "0" or nothing is all of it.
 */
export function useDecideWithdrawal(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; status: "accepted" | "rejected" | "refunded"; note: string; refundAmountMinor?: string }) =>
      unwrap(
        tenantApi(toValue(tenantId)).PUT("/v1/contract-withdrawals/{id}/status", {
          params: { path: { id: v.id } },
          body: { status: v.status, note: v.note, ...(v.refundAmountMinor && v.refundAmountMinor !== "0" ? { refund_amount_minor: v.refundAmountMinor } : {}) },
        }),
      ),
    onSuccess: () => queries.invalidateQueries({ queryKey: contractKeys.withdrawals(toValue(tenantId)) }),
  });
}

/** Asks billing to refund an accepted withdrawal (a credit note that returns the amount to the card). The answer says what happened: `refund_state` and the credit note. */
export function useRequestRefund(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; amountMinor?: string }) =>
      unwrap(tenantApi(toValue(tenantId)).POST("/v1/contract-withdrawals/{id}/refund", { params: { path: { id: v.id } }, body: v.amountMinor && v.amountMinor !== "0" ? { amount_minor: v.amountMinor } : {} })),
    onSuccess: () => queries.invalidateQueries({ queryKey: contractKeys.withdrawals(toValue(tenantId)) }),
  });
}

/** Records a statement a customer made outside the pages (a mail, a call) as a withdrawal request, from the console. `invoiceId` names the invoice they withdraw from. */
export function useRecordWithdrawal(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { customer: string; statement: string; receiveTime?: string; invoiceId?: string }) =>
      unwrap(
        tenantApi(toValue(tenantId)).POST("/v1/customers/{external_id}/contract-withdrawals", {
          params: { path: { external_id: v.customer } },
          body: { statement: v.statement, source: "console", ...(v.receiveTime ? { receive_time: v.receiveTime } : {}), ...(v.invoiceId ? { invoice_id: v.invoiceId } : {}) },
        }),
      ),
    onSuccess: () => queries.invalidateQueries({ queryKey: contractKeys.withdrawals(toValue(tenantId)) }),
  });
}

const ACCEPTANCE_PAGE = 20;

/**
 * What a customer accepted, one row per version, newest first, a page at a time (`fetchNextPage` for "Show more") and narrowed by the filter. A checkout moment's rows can
 * land on two pages; the log groups them by `group_id`, so the group completes when the next page is read.
 */
export function useContractAcceptances(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string>, filter: MaybeRefOrGetter<AcceptanceFilter>) {
  return useInfiniteQuery({
    queryKey: computed(() => [...contractKeys.acceptances(toValue(tenantId), toValue(customer)), acceptanceQuery(toValue(filter))] as const),
    initialPageParam: "",
    queryFn: ({ pageParam }) =>
      unwrap(
        tenantApi(toValue(tenantId)).GET("/v1/customers/{external_id}/contract-acceptances", {
          params: { path: { external_id: toValue(customer) }, query: { ...acceptanceQuery(toValue(filter)), page_size: ACCEPTANCE_PAGE, page_token: pageParam || undefined } },
        }),
      ),
    getNextPageParam: (last) => last.next_page_token || undefined,
    enabled: ready(tenantId, () => toValue(customer) !== ""),
  });
}

/** The invoice a withdrawal names (asked for when `id` is set): its number, currency and total, for the amount that may be refunded. */
export function useWithdrawalInvoice(tenantId: MaybeRefOrGetter<string>, id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => contractKeys.invoice(toValue(tenantId), toValue(id))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/invoices/{id}", { params: { path: { id: toValue(id) } } })),
    enabled: ready(tenantId, () => toValue(id) !== ""),
  });
}

/** The paid invoices of a customer, newest first: the ones a withdrawal can name (an invoice that was not paid has nothing to refund). */
export function useCustomerPaidInvoices(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string>, on?: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: computed(() => contractKeys.paidInvoices(toValue(tenantId), toValue(customer))),
    queryFn: async () => (await unwrap(tenantApi(toValue(tenantId)).GET("/v1/invoices", { params: { query: { customer: toValue(customer), status: "paid", page_size: PAGE.long } } }))).invoices ?? [],
    enabled: ready(tenantId, () => toValue(customer) !== "" && toValue(on ?? true)),
  });
}

/** The page an acceptance's group accepted, as stored, with its hash (asked for when `id` is set). It is the seller's evidence: show it only in a sandboxed frame. */
export function useContractSnapshot(tenantId: MaybeRefOrGetter<string>, id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => ["tenants", toValue(tenantId), "contract-snapshot", toValue(id)] as const),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/contract-acceptances/{id}/snapshot", { params: { path: { id: toValue(id) } } })),
    enabled: ready(tenantId, () => toValue(id) !== ""),
  });
}

/** What the customer has to accept now, and what of it they have: the seller's required texts as of today (the texts themselves are not shown here). */
export function useContractRequirements(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => ["tenants", toValue(tenantId), "contract-requirements", toValue(customer)] as const),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/customers/{external_id}/contract-requirements", { params: { path: { external_id: toValue(customer) } } })),
    enabled: ready(tenantId, () => toValue(customer) !== ""),
  });
}

/** The PDF bytes of a base64 string (the API sends them that way inside its JSON). */
export function pdfBlob(base64: string): Blob {
  const raw = atob(base64);
  const bytes = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return new Blob([bytes], { type: "application/pdf" });
}

/**
 * Downloads the customer's PDF copy of what they accepted (the page drawn as a PDF, ending with the hash of the page). A text the PDF font cannot draw has none:
 * "unavailable" says so and nothing is saved. Errors are thrown as an ApiError.
 */
export async function downloadSnapshotPdf(tenantId: string, acceptance: string): Promise<"saved" | "unavailable"> {
  const r = await unwrap(tenantApi(tenantId).GET("/v1/contract-acceptances/{id}/snapshot", { params: { path: { id: acceptance }, query: { pdf: true } } }));
  if (!r.pdf) return "unavailable";
  const url = URL.createObjectURL(pdfBlob(r.pdf));
  const a = document.createElement("a");
  a.href = url;
  a.download = `agreement-${r.sha256.slice(0, 12)}.pdf`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
  return "saved";
}
