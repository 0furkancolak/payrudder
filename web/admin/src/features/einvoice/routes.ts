import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function einvoiceRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "e-invoice", component: lazyPage(() => import("./EInvoicePage.vue")) })];
}
