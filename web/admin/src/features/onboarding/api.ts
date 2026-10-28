import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { useMeters } from "@/features/meters";
import { usePackages } from "@/features/packages";
import { useApiKeys, type App } from "@/features/product/api";
import { liveState, tenantOf } from "@/shared/lib/environment";
import type { Facts } from "./checklist";

/** What the "Getting started" list reads: the sandbox of the first app (its meters and packages), its keys, and whether it is live. Nothing is asked for before there is an app. */
export function useSetupFacts(app: MaybeRefOrGetter<App | undefined>) {
  const tenant = computed(() => tenantOf(toValue(app), "sandbox"));
  const appId = computed(() => toValue(app)?.id ?? "");
  const meters = useMeters(tenant);
  const packages = usePackages(tenant);
  const keys = useApiKeys(appId);
  const facts = computed<Facts>(() => ({
    apps: toValue(app) ? 1 : 0,
    meters: meters.data.value?.length ?? 0,
    packages: (packages.data.value ?? []).filter((p) => p.version).length,
    keys: (keys.data.value?.api_keys ?? []).filter((k) => k.environment === "ENVIRONMENT_SANDBOX" && k.status === "STATUS_ACTIVE").length,
    live: liveState(toValue(app)) === "active",
  }));
  return { facts, tenant, loading: computed(() => meters.isPending.value || packages.isPending.value || keys.isPending.value) };
}
