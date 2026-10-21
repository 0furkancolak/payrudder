import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function subscriptionRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "subscriptions", component: lazyPage(() => import("./SubscriptionsPage.vue")) })];
}
