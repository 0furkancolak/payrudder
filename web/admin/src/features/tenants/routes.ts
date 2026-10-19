import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import InvoicePage from "@/features/invoices/InvoicePage.vue";
import TenantPage from "./TenantPage.vue";
import TenantsPage from "./TenantsPage.vue";

/** The tenant routes, mounted under the signed-in layout. The invoice page lives under its tenant, so its route is declared here too. */
export function tenantRoutes(parent: AnyRoute) {
  const list = createRoute({ getParentRoute: () => parent, path: "/tenants", component: TenantsPage });
  const detail = createRoute({ getParentRoute: () => parent, path: "/tenants/$id", component: TenantPage });
  const invoice = createRoute({ getParentRoute: () => parent, path: "/tenants/$id/invoices/$invoiceId", component: InvoicePage });
  return [list, detail, invoice];
}
