import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function paymentRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "payments", component: lazyPage(() => import("./PaymentsPage.vue")) })];
}
