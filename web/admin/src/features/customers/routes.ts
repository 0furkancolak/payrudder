import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import CustomersPage from "./CustomersPage.vue";

export function customerRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "customers", component: CustomersPage })];
}
