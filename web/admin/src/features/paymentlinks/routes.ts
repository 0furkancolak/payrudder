import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function paymentLinkRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "payment-links", component: lazyPage(() => import("./PaymentLinksPage.vue")) })];
}
