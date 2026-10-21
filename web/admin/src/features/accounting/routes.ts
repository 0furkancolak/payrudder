import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import AccountingPage from "./AccountingPage.vue";

export function accountingRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "accounting", component: AccountingPage })];
}
