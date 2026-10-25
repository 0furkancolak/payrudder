import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

/** The invoice list and an invoice, under the operator's /tenants/$id. A person has their own invoice pages under /app/t/$id (see product/routes.ts). */
export function invoiceRoutes(tenant: AnyRoute) {
  return [
    createRoute({ getParentRoute: () => tenant, path: "invoices", component: lazyPage(() => import("./InvoicesPage.vue")) }),
    createRoute({ getParentRoute: () => tenant, path: "invoices/$invoiceId", component: lazyPage(() => import("./InvoicePage.vue")) }),
  ];
}
