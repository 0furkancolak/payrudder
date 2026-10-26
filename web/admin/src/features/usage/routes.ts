import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function usageRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "usage", component: lazyPage(() => import("./UsagePage.vue")) })];
}
