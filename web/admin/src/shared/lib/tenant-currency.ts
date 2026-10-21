import { useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { useApps } from "@/features/product/api";
import { tenantKeys } from "@/features/tenants/api";
import { api, unwrap } from "@/shared/api/client";
import { isOperator } from "@/shared/lib/session";

/**
 * The currency of one tenant, for forms that default a money field to it (a pack's price, an ad hoc link). A person reads it from the apps it
 * already loaded; an operator also reads the tenant list. Empty until known, so a form can fall back to something sensible and then switch once it arrives.
 */
export function useTenantCurrency(tenantId: MaybeRefOrGetter<string>) {
  const apps = useApps();
  const operator = useQuery({
    queryKey: tenantKeys.list,
    queryFn: () => unwrap(api.GET("/admin/v1/tenants", { params: { query: { page_size: 200 } } })),
    enabled: isOperator,
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
