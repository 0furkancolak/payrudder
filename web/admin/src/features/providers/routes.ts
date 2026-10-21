import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function providerRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "providers", component: lazyPage(() => import("./ProvidersPage.vue")) })];
}
