import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function sellerAddressRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "address", component: lazyPage(() => import("./SellerAddressPage.vue")) })];
}
