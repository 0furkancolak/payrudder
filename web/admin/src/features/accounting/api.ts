import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { toApiError, tenantApi, unwrap } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";
import { currentToken } from "@/shared/lib/session";

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
          params: { query: { app_id: toValue(app) || undefined, include_voided: toValue(includeVoided) || undefined, page_size: 200 } },
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

/** Downloads the CSV of the period. The file needs the operator's token, so it is fetched and saved from a blob, not linked. */
export async function downloadCsv(tenantId: string, from: string, to: string, app: string, granularity: string): Promise<void> {
  const query = new URLSearchParams({ from, to, granularity });
  if (app) query.set("app_id", app);
  const res = await fetch(`/gw/v1/accounting/export.csv?${query.toString()}`, {
    headers: { Authorization: `Bearer ${currentToken() ?? ""}`, "X-Payrudder-Tenant": tenantId },
  });
  if (!res.ok) throw toApiError(await res.json().catch(() => ({})));
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement("a");
  a.href = url;
  a.download = `accounting-${from}-${to}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}
