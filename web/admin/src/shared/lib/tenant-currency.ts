import { useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { useApps } from "@/features/product/api";
import { tenantKeys } from "@/features/tenants/api";
import { api, unwrap } from "@/shared/api/client";
import { personToken } from "@/shared/lib/session";

/**
 * The currency of one tenant, for forms that default a money field to it (a pack's price, an ad hoc link). A person's session reads it from the apps it
 * already loaded; the operator reads the tenant list. Empty until known, so a form can fall back to something sensible and then switch once it arrives.
 */
export function useTenantCurrency(tenantId: MaybeRefOrGetter<string>) {
  const apps = useApps();
  const operator = useQuery({
    queryKey: tenantKeys.list,
    queryFn: () => unwrap(api.GET("/admin/v1/tenants", { params: { query: { page_size: 200 } } })),
    enabled: computed(() => personToken.value === null),
  });
  return computed(() => {
    const id = toValue(tenantId);
    for (const app of apps.data.value?.apps ?? []) {
      const t = app.tenants?.find((x) => x.id === id);
      if (t?.currency) return t.currency;
    }
    return operator.data.value?.tenants?.find((x) => x.id === id)?.currency ?? "";
  });
}
