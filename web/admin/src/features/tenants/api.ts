import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { ApiError, api, unwrap, type Tenant } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";
import { findTenant } from "@/shared/lib/environment";
import { operatorView } from "@/shared/lib/session";

export const tenantKeys = {
  list: ["tenants"] as const,
  detail: (id: string) => ["tenants", id] as const,
};

/** The operator's list of tenants. `enabled` lets a caller that may be a person (who has no admin API) switch it off. */
export function useTenants(options: { enabled?: MaybeRefOrGetter<boolean> } = {}) {
  return useQuery({
    queryKey: tenantKeys.list,
    queryFn: () => unwrap(api.GET("/admin/v1/tenants", { params: { query: { page_size: PAGE.whole } } })),
    enabled: computed(() => toValue(options.enabled ?? true)),
  });
}

/** Suspends an active tenant (its API keys stop working at once) or reinstates a suspended one. The reason is kept in the log. */
export function useSetTenantStatus() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; status: "active" | "suspended"; reason?: string }) =>
      unwrap(api.PUT("/admin/v1/tenants/{id}/status", { params: { path: { id: v.id } }, body: { status: v.status, reason: v.reason ?? "" } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: tenantKeys.list }),
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
