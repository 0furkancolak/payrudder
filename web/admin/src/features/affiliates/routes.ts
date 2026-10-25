import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function affiliateRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "affiliates", component: lazyPage(() => import("./AffiliatesPage.vue")) })];
}
