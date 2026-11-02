import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";

export const paymentKeys = {
  ledger: (tenantId: string) => ["tenants", tenantId, "ledger"] as const,
};

/** The tenant's payment ledger, newest first. */
export function useLedger(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => paymentKeys.ledger(toValue(tenantId))),
    queryFn: () => unwrap(api.GET("/admin/v1/tenants/{tenant_id}/ledger", { params: { path: { tenant_id: toValue(tenantId) }, query: { limit: PAGE.whole } } })),
  });
}

/** Compares a period's paid invoices with the ledger. */
export function useReconcile(tenantId: MaybeRefOrGetter<string>) {
  return useMutation({
    mutationFn: (period: string) =>
      unwrap(api.GET("/admin/v1/tenants/{tenant_id}/reconciliation", { params: { path: { tenant_id: toValue(tenantId) }, query: { period } } })),
  });
}

// --- the reconciliation queue and settlement (docs/reconciliation.md)

export const reconcileKeys = {
  all: (tenantId: string) => ["tenants", tenantId, "reconciliation"] as const,
  list: (tenantId: string, statuses: string[]) => ["tenants", tenantId, "reconciliation", "discrepancies", statuses.join(",")] as const,
  one: (tenantId: string, id: number) => ["tenants", tenantId, "reconciliation", "discrepancy", id] as const,
  settlement: (tenantId: string, period: string) => ["tenants", tenantId, "settlement", period] as const,
};

/** How many discrepancies the console loads at a time; the list says so when the server has more. */
const QUEUE = 200;

/** The kept discrepancies in the given statuses (none: all), newest first, with the counts of every status and kind and the last run. */
export function useDiscrepancies(tenantId: MaybeRefOrGetter<string>, statuses: MaybeRefOrGetter<string[]>) {
  return useQuery({
    queryKey: computed(() => reconcileKeys.list(toValue(tenantId), toValue(statuses))),
    queryFn: () =>
      unwrap(
        api.GET("/admin/v1/tenants/{tenant_id}/reconciliation/discrepancies", {
          params: { path: { tenant_id: toValue(tenantId) }, query: { limit: QUEUE, ...(toValue(statuses).length ? { status: toValue(statuses).join(",") } : {}) } },
        }),
      ),
    placeholderData: keepPreviousData,
  });
}

/** One discrepancy with its history. */
export function useDiscrepancy(tenantId: MaybeRefOrGetter<string>, id: MaybeRefOrGetter<number | undefined>) {
  return useQuery({
    queryKey: computed(() => reconcileKeys.one(toValue(tenantId), toValue(id) ?? 0)),
    queryFn: () => unwrap(api.GET("/admin/v1/tenants/{tenant_id}/reconciliation/discrepancies/{id}", { params: { path: { tenant_id: toValue(tenantId), id: toValue(id) ?? 0 } } })),
    enabled: computed(() => toValue(id) !== undefined),
  });
}

/** Compares a period and keeps what disagrees. */
export function useRunReconciliation(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { period: string; environment: "sandbox" | "live" }) =>
      unwrap(api.POST("/admin/v1/tenants/{tenant_id}/reconciliation/runs", { params: { path: { tenant_id: toValue(tenantId) }, query: { environment: v.environment } }, body: { period: v.period } })),
    onSuccess: () => Promise.all([queries.invalidateQueries({ queryKey: reconcileKeys.all(toValue(tenantId)) }), queries.invalidateQueries({ queryKey: ["tenants", toValue(tenantId), "settlement"] })]),
  });
}

/** What an operator does to one discrepancy. */
export function useDiscrepancyActions(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const path = (id: number) => ({ params: { path: { tenant_id: toValue(tenantId), id } } });
  const done = () => queries.invalidateQueries({ queryKey: reconcileKeys.all(toValue(tenantId)) });
  return {
    assign: useMutation({ mutationFn: (v: { id: number; owner: string }) => unwrap(api.POST("/admin/v1/tenants/{tenant_id}/reconciliation/discrepancies/{id}/assign", { ...path(v.id), body: { owner: v.owner } })), onSuccess: done }),
    explain: useMutation({
      mutationFn: (v: { id: number; note: string; owner?: string }) => unwrap(api.POST("/admin/v1/tenants/{tenant_id}/reconciliation/discrepancies/{id}/explain", { ...path(v.id), body: { note: v.note, owner: v.owner } })),
      onSuccess: done,
    }),
    resolve: useMutation({
      mutationFn: (v: { id: number; resolution: string; note: string; owner?: string; ledgerFact?: { provider: string; provider_ref: string } }) =>
        unwrap(
          api.POST("/admin/v1/tenants/{tenant_id}/reconciliation/discrepancies/{id}/resolve", {
            ...path(v.id),
            body: { resolution: v.resolution as "other", note: v.note, owner: v.owner, ledger_fact: v.ledgerFact },
          }),
        ),
      onSuccess: done,
    }),
  };
}

/** The period's successful payments with what is known of their settlement. */
export function useSettlementPayments(tenantId: MaybeRefOrGetter<string>, period: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => reconcileKeys.settlement(toValue(tenantId), toValue(period))),
    queryFn: () => unwrap(api.GET("/admin/v1/tenants/{tenant_id}/settlement/payments", { params: { path: { tenant_id: toValue(tenantId) }, query: { period: toValue(period) } } })),
  });
}

/** Imports a settlement report (CSV text) from a provider or the bank; the whole file or nothing. */
export function useImportSettlement(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { csv: string; environment: "sandbox" | "live"; provider: string }) =>
      unwrap(
        api.POST("/admin/v1/tenants/{tenant_id}/settlement/import", {
          params: { path: { tenant_id: toValue(tenantId) }, query: { environment: v.environment, ...(v.provider ? { provider: v.provider } : {}) } },
          body: v.csv as never,
          bodySerializer: (b: unknown) => String(b),
          headers: { "Content-Type": "text/csv" },
        }),
      ),
    onSuccess: () => queries.invalidateQueries({ queryKey: ["tenants", toValue(tenantId), "settlement"] }),
  });
}
