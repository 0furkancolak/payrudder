import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";
import type { components } from "@/shared/api/schema";

export type ExpenseInput = components["schemas"]["ExpenseInput"];
export type Expense = components["schemas"]["Expense"];
export type Category = components["schemas"]["ExpenseCategory"];

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
