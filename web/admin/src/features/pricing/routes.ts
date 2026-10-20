import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import PricingPage from "./PricingPage.vue";

export function pricingRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "pricing", component: PricingPage })];
}
