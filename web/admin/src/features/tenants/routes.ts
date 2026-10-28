import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { dashSearch } from "@/features/dashboard/lib";
import { lazyPage } from "@/shared/lib/lazy";
import { accountingRoutes } from "@/features/accounting/routes";
import { affiliateRoutes } from "@/features/affiliates/routes";
import { appRoutes } from "@/features/apps";
import { creditRoutes } from "@/features/credits/routes";
import { customerRoutes } from "@/features/customers/routes";
import { einvoiceRoutes } from "@/features/einvoice/routes";
import { invoiceRoutes } from "@/features/invoices/routes";
import { mailRoutes } from "@/features/mail/routes";
import { meterRoutes } from "@/features/meters/routes";
import { packageRoutes } from "@/features/packages/routes";
import { paymentRoutes } from "@/features/payments/routes";
import { pricingRoutes } from "@/features/pricing/routes";
import { providerRoutes } from "@/features/providers/routes";
import { reportRoutes } from "@/features/reports/routes";
import { sellerAddressRoutes } from "@/features/seller-address";
import { subscriptionRoutes } from "@/features/subscriptions/routes";
import { usageRoutes } from "@/features/usage/routes";
import { paymentLinkRoutes } from "@/features/paymentlinks/routes";
import { webhookRoutes } from "@/features/webhooks/routes";
import { connectRoutes } from "@/features/connect/routes";

/**
 * The tenant routes, mounted under the signed-in layout. A tenant is a layout (its header and tabs) with a page per tab; the invoice page lives
 * under it too, so its route is declared here. The billing tabs are features of their own and contribute their routes.
 */
export function tenantRoutes(parent: AnyRoute) {
  const list = createRoute({ getParentRoute: () => parent, path: "/tenants", component: lazyPage(() => import("./TenantsPage.vue")) });
  const tenant = createRoute({ getParentRoute: () => parent, path: "/tenants/$id", component: lazyPage(() => import("./TenantLayout.vue")) });
  const overview = createRoute({ getParentRoute: () => tenant, path: "/", component: lazyPage(() => import("./TenantOverview.vue")), validateSearch: dashSearch });
  return [
    list,
    tenant.addChildren([overview, ...invoiceRoutes(tenant), ...customerRoutes(tenant), ...meterRoutes(tenant), ...packageRoutes(tenant), ...subscriptionRoutes(tenant), ...usageRoutes(tenant), ...pricingRoutes(tenant), ...paymentRoutes(tenant), ...creditRoutes(tenant), ...accountingRoutes(tenant), ...affiliateRoutes(tenant), ...providerRoutes(tenant), ...reportRoutes(tenant), ...paymentLinkRoutes(tenant), ...webhookRoutes(tenant), ...connectRoutes(tenant), ...appRoutes(tenant), ...sellerAddressRoutes(tenant), ...mailRoutes(tenant), ...einvoiceRoutes(tenant)]),
  ];
}
