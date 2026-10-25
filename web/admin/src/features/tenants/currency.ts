import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { useApps } from "@/features/product/api";
import { findTenant } from "@/shared/lib/environment";
import { isOperator } from "@/shared/lib/session";
import { useTenants } from "./api";

/**
 * The currency of one tenant, for forms that default a money field to it (a pack's price, an ad hoc link). A person reads it from the apps it
 * already loaded; an operator also reads the tenant list. Empty until known, so a form can fall back to something sensible and then switch once it arrives.
 */
export function useTenantCurrency(tenantId: MaybeRefOrGetter<string>) {
  const apps = useApps();
  const operator = useTenants({ enabled: isOperator });
  return computed(() => {
    const id = toValue(tenantId);
    const own = findTenant(apps.data.value?.apps, id)?.tenant.currency;
    if (own) return own;
    return operator.data.value?.tenants?.find((x) => x.id === id)?.currency ?? "";
  });
}
