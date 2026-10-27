import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";
import type { components } from "@/shared/api/schema";

export type ExpenseInput = components["schemas"]["ExpenseInput"];
export type Expense = components["schemas"]["Expense"];
export type Category = components["schemas"]["ExpenseCategory"];
/** The block of the summary for one currency. */
export type CurrencySummary = components["schemas"]["AccountingCurrency"];

export const CATEGORIES: readonly Category[] = ["infrastructure", "payment_fees", "marketing", "payroll", "tax", "software", "other"];

export const accountingKeys = {
  all: (tenantId: string) => ["tenants", tenantId, "accounting"] as const,
  summary: (tenantId: string, from: string, to: string, app: string, gran: string) => ["tenants", tenantId, "accounting", "summary", from, to, app, gran] as const,
  expenses: (tenantId: string, app: string, includeVoided: boolean) => ["tenants", tenantId, "accounting", "expenses", app, includeVoided] as const,
};

/** Income and expenses of a period, one block per currency. */
export function useAccountingSummary(
  tenantId: MaybeRefOrGetter<string>,
  from: MaybeRefOrGetter<string>,
  to: MaybeRefOrGetter<string>,
  app: MaybeRefOrGetter<string>,
  granularity: MaybeRefOrGetter<"day" | "month">,
  enabled: MaybeRefOrGetter<boolean> = true,
) {
  return useQuery({
    queryKey: computed(() => accountingKeys.summary(toValue(tenantId), toValue(from), toValue(to), toValue(app), toValue(granularity))),
    queryFn: () =>
      unwrap(
        tenantApi(toValue(tenantId)).GET("/v1/accounting/summary", {
          params: { query: { from: toValue(from), to: toValue(to), app_id: toValue(app) || undefined, granularity: toValue(granularity) } },
        }),
      ),
    enabled: computed(() => toValue(enabled)),
  });
}

/** The expenses as recorded (a recurring one once), newest first. */
export function useExpenses(tenantId: MaybeRefOrGetter<string>, app: MaybeRefOrGetter<string>, includeVoided: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: computed(() => accountingKeys.expenses(toValue(tenantId), toValue(app), toValue(includeVoided))),
    queryFn: () =>
      unwrap(
        tenantApi(toValue(tenantId)).GET("/v1/expenses", {
          params: { query: { app_id: toValue(app) || undefined, include_voided: toValue(includeVoided) || undefined, page_size: PAGE.whole } },
        }),
      ),
  });
}

/** Saves an expense: a new one, or the replacement of `id`. Both refresh every accounting number of the tenant. */
export function useSaveExpense(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { id?: string; body: ExpenseInput }) => {
      const api = tenantApi(toValue(tenantId));
      return v.id ? unwrap(api.PUT("/v1/expenses/{id}", { params: { path: { id: v.id } }, body: v.body })) : unwrap(api.POST("/v1/expenses", { body: v.body }));
    },
    onSuccess: () => queries.invalidateQueries({ queryKey: accountingKeys.all(toValue(tenantId)) }),
  });
}

/** Voids an expense with a reason; it leaves the summaries and stays in the list of voided entries. */
export function useVoidExpense(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; reason: string }) =>
      unwrap(tenantApi(toValue(tenantId)).DELETE("/v1/expenses/{id}", { params: { path: { id: v.id }, query: { reason: v.reason } } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: accountingKeys.all(toValue(tenantId)) }),
  });
}

/** Downloads a CSV of the period (the series, or with `coupons` the report per coupon). The file needs the session token, so it is fetched through the API client and saved from a blob, not linked. */
export async function downloadCsv(tenantId: string, from: string, to: string, app: string, granularity: "day" | "month", report: "export" | "coupons" = "export"): Promise<void> {
  const blob = await unwrap(
    tenantApi(tenantId).GET(report === "coupons" ? "/v1/accounting/coupons.csv" : "/v1/accounting/export.csv", {
      params: { query: { from, to, granularity, ...(app ? { app_id: app } : {}) } },
      parseAs: "blob",
    }),
  );
  const url = URL.createObjectURL(blob as Blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `${report === "coupons" ? "coupons" : "accounting"}-${from}-${to}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}

export type ExpenseRule = components["schemas"]["ExpenseRule"];
export type ExpenseRuleInput = components["schemas"]["ExpenseRuleInput"];
export type InboxItem = components["schemas"]["InboxItem"];

const inboxKeys = {
  inbox: (tenantId: string, app: string) => ["tenants", tenantId, "accounting", "inbox", app] as const,
  items: (tenantId: string) => ["tenants", tenantId, "accounting", "inbox-items"] as const,
  rules: (tenantId: string) => ["tenants", tenantId, "accounting", "rules"] as const,
};

/** The address invoices are forwarded to (made on first use). */
export function useExpenseInbox(tenantId: MaybeRefOrGetter<string>, app: MaybeRefOrGetter<string> = "") {
  return useQuery({
    queryKey: computed(() => inboxKeys.inbox(toValue(tenantId), toValue(app))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/expense-inbox", { params: { query: { app_id: toValue(app) || undefined } } })),
  });
}

/** Gives the inbox a new address; the old one stops working. */
export function useRotateInbox(tenantId: MaybeRefOrGetter<string>, app: MaybeRefOrGetter<string> = "") {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: () => unwrap(tenantApi(toValue(tenantId)).POST("/v1/expense-inbox/rotate", { params: { query: { app_id: toValue(app) || undefined } } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: inboxKeys.inbox(toValue(tenantId), toValue(app)) }),
  });
}

/** What mail brought and waits for a person. */
export function useInboxItems(tenantId: MaybeRefOrGetter<string>, status: MaybeRefOrGetter<"to_review" | "accepted" | "dismissed"> = "to_review") {
  return useQuery({
    queryKey: computed(() => [...inboxKeys.items(toValue(tenantId)), toValue(status)]),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/expense-inbox/items", { params: { query: { status: toValue(status), page_size: PAGE.whole } } })),
    refetchOnWindowFocus: true,
  });
}

/** The bytes of a received attachment, as a File the scanner reads. */
export async function fetchInboxFile(tenantId: string, item: InboxItem): Promise<File> {
  const blob = (await unwrap(tenantApi(tenantId).GET("/v1/expense-inbox/items/{id}/file", { params: { path: { id: item.id ?? "" } }, parseAs: "blob" }))) as Blob;
  return new File([blob], item.filename ?? "attachment", { type: item.content_type ?? blob.type });
}

/** Marks a received attachment accepted (with the expense made from it) or dismissed. */
export function useResolveInboxItem(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; status: "accepted" | "dismissed"; expenseId?: string }) =>
      unwrap(tenantApi(toValue(tenantId)).POST("/v1/expense-inbox/items/{id}/resolve", { params: { path: { id: v.id } }, body: { status: v.status, expense_id: v.expenseId } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: inboxKeys.items(toValue(tenantId)) }),
  });
}

/** The recurring-vendor rules. */
export function useExpenseRules(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => inboxKeys.rules(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/expense-rules")),
  });
}

export function useSaveRule(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { id?: string; body: ExpenseRuleInput }) => {
      const api = tenantApi(toValue(tenantId));
      return v.id ? unwrap(api.PUT("/v1/expense-rules/{id}", { params: { path: { id: v.id } }, body: v.body })) : unwrap(api.POST("/v1/expense-rules", { body: v.body }));
    },
    onSuccess: () => queries.invalidateQueries({ queryKey: inboxKeys.rules(toValue(tenantId)) }),
  });
}

export function useDeleteRule(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => unwrap(tenantApi(toValue(tenantId)).DELETE("/v1/expense-rules/{id}", { params: { path: { id } } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: inboxKeys.rules(toValue(tenantId)) }),
  });
}

/** Stores a receipt as a general asset of the tenant and returns its id (the attachment of an expense). Throws when the file is rejected. */
export async function uploadReceipt(tenantId: string, file: File): Promise<string> {
  const form = new FormData();
  form.append("file", file, file.name);
  const res = (await unwrap(tenantApi(tenantId).POST("/v1/assets", { body: form as never, bodySerializer: (b: unknown) => b as FormData }))) as {
    results?: { status?: string; message?: string; asset?: { id?: string } }[];
  };
  const r = res.results?.[0];
  if (r?.status !== "created" || !r.asset?.id) throw new Error(r?.message || "rejected");
  return r.asset.id;
}

/** A presigned link to a stored receipt (valid for minutes). */
export async function receiptUrl(tenantId: string, assetId: string): Promise<string> {
  const res = (await unwrap(tenantApi(tenantId).GET("/v1/assets/{id}/download", { params: { path: { id: assetId } } }))) as { url?: string };
  return res.url ?? "";
}
