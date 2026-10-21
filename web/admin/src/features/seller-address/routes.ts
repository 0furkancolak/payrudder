import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import SellerAddressPage from "./SellerAddressPage.vue";

export function sellerAddressRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "address", component: SellerAddressPage })];
}
