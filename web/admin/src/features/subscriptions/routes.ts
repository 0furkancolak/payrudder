import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import SubscriptionsPage from "./SubscriptionsPage.vue";

export function subscriptionRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "subscriptions", component: SubscriptionsPage })];
}
