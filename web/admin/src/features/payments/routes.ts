import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import PaymentsPage from "./PaymentsPage.vue";

export function paymentRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "payments", component: PaymentsPage })];
}
