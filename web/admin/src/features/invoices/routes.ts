import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

/** The invoice list and an invoice, under a tenant (the operator's /tenants/$id and the person's /app/t/$id). */
export function invoiceRoutes(tenant: AnyRoute) {
  return [
    createRoute({ getParentRoute: () => tenant, path: "invoices", component: lazyPage(() => import("./InvoicesPage.vue")) }),
    createRoute({ getParentRoute: () => tenant, path: "invoices/$invoiceId", component: lazyPage(() => import("./InvoicePage.vue")) }),
  ];
}
