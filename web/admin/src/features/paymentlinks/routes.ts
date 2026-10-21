import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import PaymentLinksPage from "./PaymentLinksPage.vue";

export function paymentLinkRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "payment-links", component: PaymentLinksPage })];
}
