import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

/** The invoice list, under the operator's /tenants/$id; an invoice opens in a sheet of the list (`?invoice=<id>` opens one). A person has their own list under /app/t/$id (see product/routes.ts). */
export function invoiceRoutes(tenant: AnyRoute) {
  return [
    createRoute({ getParentRoute: () => tenant, path: "invoices", component: lazyPage(() => import("./InvoicesPage.vue")) }),
  ];
}
