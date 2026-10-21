import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function customerRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "customers", component: lazyPage(() => import("./CustomersPage.vue")) })];
}
