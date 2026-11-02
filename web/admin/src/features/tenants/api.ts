import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { ApiError, api, unwrap, type Tenant } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";
import { restOfPages } from "@/shared/lib/bulk";
import { PAGE } from "@/shared/api/limits";
import { findTenant } from "@/shared/lib/environment";
import { operatorView } from "@/shared/lib/session";

export const tenantKeys = {
  list: ["tenants"] as const,
  detail: (id: string) => ["tenants", id] as const,
  statusEvents: (id: string) => ["tenants", id, "status-events"] as const,
  lifecycle: (id: string) => ["tenants", id, "lifecycle"] as const,
  appeals: ["tenants", "appeals"] as const,
  hold: (appId: string) => ["apps", appId, "hold"] as const,
};

/** The operator's list of tenants. `enabled` lets a caller that may be a person (who has no admin API) switch it off. */
export function useTenants(options: { enabled?: MaybeRefOrGetter<boolean> } = {}) {
  return useQuery({
    queryKey: tenantKeys.list,
    queryFn: () => unwrap(api.GET("/admin/v1/tenants", { params: { query: { page_size: PAGE.whole } } })),
    enabled: computed(() => toValue(options.enabled ?? true)),
  });
}

/** The tenants after the first page, fetched by page token into the list's cache: "select all N results" must cover every tenant the filters match, not the first page only. */
export function useRestOfTenants() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const first = queries.getQueryData<components["schemas"]["AdminListTenantsResponse"]>(tenantKeys.list);
      const rest = await restOfPages(first?.next_page_token ?? "", async (token) => {
        const page = await unwrap(api.GET("/admin/v1/tenants", { params: { query: { page_size: PAGE.whole, page_token: token } } }));
        return { rows: page.tenants ?? [], next: page.next_page_token ?? "" };
      });
      queries.setQueryData(tenantKeys.list, { tenants: [...(first?.tenants ?? []), ...rest], next_page_token: "" });
    },
  });
}

export type TenantStatusWord = "active" | "suspended" | "frozen" | "banned";
export type StatusCategory = "nonpayment" | "fraud" | "abuse" | "legal" | "request" | "other";
export const STATUS_CATEGORIES: readonly StatusCategory[] = ["nonpayment", "fraud", "abuse", "legal", "request", "other"];

/**
 * Suspends, freezes or bans an active tenant, or reinstates one. Suspended and banned stop the API keys at once; frozen leaves them read-only; all three take no payment
 * and send no mail or webhook. The reason and category are kept in the tenant's history, with who made the change. Where a ban needs a second operator, the first request
 * answers 409 plans.tenant.approval_pending (it is recorded).
 */
export function useSetTenantStatus() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; status: TenantStatusWord; reason?: string; category?: StatusCategory }) =>
      unwrap(api.PUT("/admin/v1/tenants/{id}/status", { params: { path: { id: v.id } }, body: { status: v.status, reason: v.reason ?? "", ...(v.category ? { category: v.category } : {}) } })),
    onSettled: (_data, _err, v) => {
      void queries.invalidateQueries({ queryKey: tenantKeys.list });
      void queries.invalidateQueries({ queryKey: tenantKeys.statusEvents(v.id) });
      void queries.invalidateQueries({ queryKey: tenantKeys.detail(v.id) });
    },
  });
}

/** The changes of a tenant's status (and the bans waiting for a second operator), newest first. */
export function useTenantStatusEvents(id: MaybeRefOrGetter<string>, enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => tenantKeys.statusEvents(toValue(id))),
    queryFn: () => unwrap(api.GET("/admin/v1/tenants/{id}/status-events", { params: { path: { id: toValue(id) } } })),
    enabled: computed(() => toValue(id) !== "" && toValue(enabled)),
  });
}

/**
 * One tenant. The operator reads it from the admin API; a person has no admin API, but the apps they own carry their tenants (with currency, time zone
 * and environment), so the same pages work for both by asking the product API and picking the tenant out.
 */
async function personTenant(id: string): Promise<Tenant> {
  const apps = await unwrap(api.GET("/app/v1/apps"));
  const hit = findTenant(apps.apps, id);
  if (!hit) throw new ApiError("tenant not found", "NOT_FOUND");
  return hit.tenant;
}

export function useTenant(id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => [...tenantKeys.detail(toValue(id)), operatorView() ? "operator" : "person"]),
    queryFn: () => (operatorView() ? unwrap(api.GET("/admin/v1/tenants/{id}", { params: { path: { id: toValue(id) } } })) : personTenant(toValue(id))),
    enabled: computed(() => toValue(id) !== ""),
  });
}

/** Looks a replay or re-rating job up by id. */
export async function lookupJob(kind: "replay" | "rerating", tenantId: string, jobId: string): Promise<unknown> {
  if (kind === "replay") {
    return unwrap(api.GET("/admin/v1/tenants/{tenant_id}/replays/{job_id}", { params: { path: { tenant_id: tenantId, job_id: jobId } } }));
  }
  return unwrap(api.GET("/admin/v1/tenants/{tenant_id}/rerating-jobs/{job_id}", { params: { path: { tenant_id: tenantId, job_id: jobId } } }));
}

export const pauseKey = ["price-change-pause"] as const;

/** Whether the operator has paused every price change of the platform (plan 15), who paused them and why. */
export function usePriceChangePause() {
  return useQuery({ queryKey: pauseKey, queryFn: () => unwrap(api.GET("/admin/v1/price-change-pause")) });
}

/** Pulls or releases the kill switch of price changes. The reason is kept with the setting and in the audit log. */
export function useSetPriceChangePause() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { paused: boolean; reason?: string }) => unwrap(api.PUT("/admin/v1/price-change-pause", { body: { paused: v.paused, reason: v.reason ?? "" } })),
    onSuccess: (data) => queries.setQueryData(pauseKey, data),
  });
}

// --- retention, schedule and appeals (plan 14)

export type TenantAppeal = components["schemas"]["TenantAppeal"];
export type TenantLifecycle = components["schemas"]["TenantLifecycle"];
export type AppHold = components["schemas"]["AppHold"];

/** A live tenant's retention deadline and the status scheduled for it (the operator's). */
export function useTenantLifecycle(id: MaybeRefOrGetter<string>, enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => tenantKeys.lifecycle(toValue(id))),
    queryFn: () => unwrap(api.GET("/admin/v1/tenants/{id}/lifecycle", { params: { path: { id: toValue(id) } } })),
    enabled: computed(() => toValue(id) !== "" && toValue(enabled)),
  });
}

export function useScheduleTenantStatus() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; status: "suspended" | "frozen"; time: string; category?: StatusCategory; reason?: string }) =>
      unwrap(api.PUT("/admin/v1/tenants/{id}/scheduled-status", { params: { path: { id: v.id } }, body: { status: v.status, schedule_time: v.time, ...(v.category ? { category: v.category } : {}), reason: v.reason ?? "" } })),
    onSuccess: (_d, v) => queries.invalidateQueries({ queryKey: tenantKeys.lifecycle(v.id) }),
  });
}

export function useCancelScheduledTenantStatus() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => unwrap(api.DELETE("/admin/v1/tenants/{id}/scheduled-status", { params: { path: { id } } })),
    onSuccess: (_d, id) => queries.invalidateQueries({ queryKey: tenantKeys.lifecycle(id) }),
  });
}

/** The owners' appeals, newest first (the latest 200): of one tenant, or of every tenant, of one status or of all. */
export function useTenantAppeals(filter: MaybeRefOrGetter<{ tenantId?: string; status?: "open" | "accepted" | "rejected" }> = {}, enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => [...tenantKeys.appeals, toValue(filter)] as const),
    queryFn: () => unwrap(api.GET("/admin/v1/appeals", { params: { query: { tenant_id: toValue(filter).tenantId || undefined, status: toValue(filter).status } } })),
    enabled: computed(() => toValue(enabled)),
  });
}

/** Accepts (the tenant is reinstated) or rejects (an answer is required) an open appeal. */
export function useDecideTenantAppeal() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; decision: "accepted" | "rejected"; answer: string }) =>
      unwrap(api.POST("/admin/v1/appeals/{id}/decision", { params: { path: { id: v.id } }, body: { decision: v.decision, answer: v.answer } })),
    onSettled: () => {
      void queries.invalidateQueries({ queryKey: tenantKeys.appeals });
      void queries.invalidateQueries({ queryKey: tenantKeys.list });
      void queries.invalidateQueries({ queryKey: ["tenants"] });
    },
  });
}

/** What the operator has done to an app's live environment as its owners may know it, and the latest appeal (the person's). */
export function useAppHold(appId: MaybeRefOrGetter<string>, enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => tenantKeys.hold(toValue(appId))),
    queryFn: () => unwrap(api.GET("/app/v1/apps/{id}/hold", { params: { path: { id: toValue(appId) } } })),
    enabled: computed(() => toValue(appId) !== "" && toValue(enabled)),
  });
}

export function useSubmitAppeal(appId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (statement: string) => unwrap(api.POST("/app/v1/apps/{id}/appeal", { params: { path: { id: toValue(appId) } }, body: { statement } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: tenantKeys.hold(toValue(appId)) }),
  });
}

export const noticeKey = ["price-change-notice"] as const;

/** The notice rules of price changes (plan 15): the default notice and the least notice per country. Not legal advice. */
export function usePriceChangeNotice() {
  return useQuery({ queryKey: noticeKey, queryFn: () => unwrap(api.GET("/admin/v1/price-change-notice")) });
}

/** Replaces the default notice and the whole table of the least notice per country. */
export function useSetPriceChangeNotice() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: components["schemas"]["SetPriceChangeNoticeRequest"]) => unwrap(api.PUT("/admin/v1/price-change-notice", { body: v })),
    onSuccess: (data) => queries.setQueryData(noticeKey, data),
  });
}
