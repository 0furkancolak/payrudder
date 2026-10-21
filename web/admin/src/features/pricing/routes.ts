import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function pricingRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "pricing", component: lazyPage(() => import("./PricingPage.vue")) })];
}
