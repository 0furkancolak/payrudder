import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function accountingRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "accounting", component: lazyPage(() => import("./AccountingPage.vue")) })];
}
