import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import ProvidersPage from "./ProvidersPage.vue";

export function providerRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "providers", component: ProvidersPage })];
}
