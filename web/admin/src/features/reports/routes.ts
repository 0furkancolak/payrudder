import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import ReportsPage from "./ReportsPage.vue";

export function reportRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "reports", component: ReportsPage })];
}
