import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

/** Two pages: the tax rates and payment retries (pricing), and the coupons with their promotion codes and redemptions (coupons). */
export function pricingRoutes(tenant: AnyRoute) {
  return [
    createRoute({ getParentRoute: () => tenant, path: "pricing", component: lazyPage(() => import("./PricingPage.vue")) }),
    createRoute({ getParentRoute: () => tenant, path: "coupons", component: lazyPage(() => import("./CouponsPage.vue")) }),
  ];
}
