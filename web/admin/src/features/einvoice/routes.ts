import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import EInvoicePage from "./EInvoicePage.vue";

export function einvoiceRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "e-invoice", component: EInvoicePage })];
}
