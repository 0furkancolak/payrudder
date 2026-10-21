import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import AppsPage from "./AppsPage.vue";

export function appRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "apps", component: AppsPage })];
}
