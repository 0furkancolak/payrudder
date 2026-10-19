import { useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";

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

export function useTenant(id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => tenantKeys.detail(toValue(id))),
    queryFn: () => unwrap(api.GET("/admin/v1/tenants/{id}", { params: { path: { id: toValue(id) } } })),
  });
}

/** Looks a replay or re-rating job up by id. */
export async function lookupJob(kind: "replay" | "rerating", tenantId: string, jobId: string): Promise<unknown> {
  if (kind === "replay") {
    return unwrap(api.GET("/admin/v1/tenants/{tenant_id}/replays/{job_id}", { params: { path: { tenant_id: tenantId, job_id: jobId } } }));
  }
  return unwrap(api.GET("/admin/v1/tenants/{tenant_id}/rerating-jobs/{job_id}", { params: { path: { tenant_id: tenantId, job_id: jobId } } }));
}
