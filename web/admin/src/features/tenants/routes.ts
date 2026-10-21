import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { appRoutes } from "@/features/apps";
import { customerRoutes } from "@/features/customers/routes";
import InvoicePage from "@/features/invoices/InvoicePage.vue";
import { mailRoutes } from "@/features/mail/routes";
import { packageRoutes } from "@/features/packages/routes";
import { paymentRoutes } from "@/features/payments/routes";
import { pricingRoutes } from "@/features/pricing/routes";
import { providerRoutes } from "@/features/providers/routes";
import { reportRoutes } from "@/features/reports/routes";
import { sellerAddressRoutes } from "@/features/seller-address";
import { subscriptionRoutes } from "@/features/subscriptions/routes";
import { paymentLinkRoutes } from "@/features/paymentlinks/routes";
import { webhookRoutes } from "@/features/webhooks/routes";
import TenantLayout from "./TenantLayout.vue";
import TenantOverview from "./TenantOverview.vue";
import TenantsPage from "./TenantsPage.vue";

/**
 * The tenant routes, mounted under the signed-in layout. A tenant is a layout (its header and tabs) with a page per tab; the invoice page lives
 * under it too, so its route is declared here. The billing tabs are features of their own and contribute their routes.
 */
export function tenantRoutes(parent: AnyRoute) {
  const list = createRoute({ getParentRoute: () => parent, path: "/tenants", component: TenantsPage });
  const tenant = createRoute({ getParentRoute: () => parent, path: "/tenants/$id", component: TenantLayout });
  const overview = createRoute({ getParentRoute: () => tenant, path: "/", component: TenantOverview });
  const invoice = createRoute({ getParentRoute: () => tenant, path: "invoices/$invoiceId", component: InvoicePage });
  return [
    list,
    tenant.addChildren([overview, invoice, ...customerRoutes(tenant), ...packageRoutes(tenant), ...subscriptionRoutes(tenant), ...pricingRoutes(tenant), ...paymentRoutes(tenant), ...providerRoutes(tenant), ...reportRoutes(tenant), ...paymentLinkRoutes(tenant), ...webhookRoutes(tenant), ...appRoutes(tenant), ...sellerAddressRoutes(tenant), ...mailRoutes(tenant)]),
  ];
}
