import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function reportRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "reports", component: lazyPage(() => import("./ReportsPage.vue")) })];
}
