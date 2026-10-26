import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function meterRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "meters", component: lazyPage(() => import("./MetersPage.vue")) })];
}
