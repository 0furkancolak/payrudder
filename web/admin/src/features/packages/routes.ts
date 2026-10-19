import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import PackagesPage from "./PackagesPage.vue";

export function packageRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "packages", component: PackagesPage })];
}
