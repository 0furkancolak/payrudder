import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function contractRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "contracts", component: lazyPage(() => import("./ContractsPage.vue")) })];
}
