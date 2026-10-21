import { useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { ApiError, api, unwrap, type Tenant } from "@/shared/api/client";
import { operatorView } from "@/shared/lib/session";

export const tenantKeys = {
  list: ["tenants"] as const,
  detail: (id: string) => ["tenants", id] as const,
};

export function useTenants() {
  return useQuery({
    queryKey: tenantKeys.list,
    queryFn: () => unwrap(api.GET("/admin/v1/tenants", { params: { query: { page_size: 200 } } })),
  });
}

/**
 * One tenant. The operator reads it from the admin API; a person has no admin API, but the apps they own carry their tenants (with currency, time zone
 * and environment), so the same pages work for both by asking the product API and picking the tenant out.
 */
async function personTenant(id: string): Promise<Tenant> {
  const apps = await unwrap(api.GET("/app/v1/apps"));
  for (const app of apps.apps ?? []) {
    const hit = app.tenants?.find((x) => x.id === id);
    if (hit) return hit;
  }
  throw new ApiError("tenant not found", "NOT_FOUND");
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
