import { createRoute, redirect, type AnyRoute } from "@tanstack/vue-router";
import { accountingRoutes } from "@/features/accounting/routes";
import { affiliateRoutes } from "@/features/affiliates/routes";
import { creditRoutes } from "@/features/credits/routes";
import { customerRoutes } from "@/features/customers/routes";
import { mailRoutes } from "@/features/mail/routes";
import { packageRoutes } from "@/features/packages/routes";
import { paymentLinkRoutes } from "@/features/paymentlinks/routes";
import { pricingRoutes } from "@/features/pricing/routes";
import { reportRoutes } from "@/features/reports/routes";
import { subscriptionRoutes } from "@/features/subscriptions/routes";
import { webhookRoutes } from "@/features/webhooks/routes";
import { dashSearch } from "@/features/dashboard/lib";
import { lazyPage } from "@/shared/lib/lazy";
import { isPerson } from "@/shared/lib/session";
import { OAUTH_ERRORS } from "./lib";

// The product's pages are loaded when first visited, so the public pages stay small. The deeper views reuse the console's own route factories.

/** beforeLoad for the pages that need a signed-in person. */
export function requirePerson(): void {
  if (!isPerson.value) throw redirect({ to: "/signin" });
}

/** beforeLoad for the sign-in and sign-up pages and for "/": somebody who is signed in goes to their home. */
export function redirectHome(): void {
  if (isPerson.value) throw redirect({ to: "/app" });
}

/** The public pages: sign in, sign up, forgot and reset password, verify e-mail (and "/", which is the sign-in page for a visitor and a redirect for somebody signed in). */
export function publicProductRoutes(root: AnyRoute) {
  const signIn = lazyPage(() => import("./SignInPage.vue"));
  const signUp = lazyPage(() => import("./SignUpPage.vue"));
  const validateSignIn = (s: Record<string, unknown>) => ({
    ...(s.expired === "1" ? { expired: "1" } : {}),
    // Why a login with Google ended without a session (set by the gateway's callback): one of a fixed few, anything else is ignored.
    ...(typeof s.oauth_error === "string" && OAUTH_ERRORS.includes(s.oauth_error) ? { oauth_error: s.oauth_error } : {}),
  });
  return [
    createRoute({ getParentRoute: () => root, path: "/", beforeLoad: redirectHome, component: signIn, validateSearch: validateSignIn }),
    createRoute({ getParentRoute: () => root, path: "/signin", beforeLoad: redirectHome, component: signIn, validateSearch: validateSignIn }),
    createRoute({ getParentRoute: () => root, path: "/signup", beforeLoad: redirectHome, component: signUp }),
    // Where the gateway sends the browser after Google: the session token is in the URL fragment, read once and removed.
    createRoute({ getParentRoute: () => root, path: "/signin/complete", component: lazyPage(() => import("./SignInCompletePage.vue")) }),
    createRoute({ getParentRoute: () => root, path: "/forgot", beforeLoad: redirectHome, component: lazyPage(() => import("./ForgotPage.vue")) }),
    // The pages the mails open work signed in or out: the token in the link is the credential, and a person may follow it from any browser.
    createRoute({ getParentRoute: () => root, path: "/reset", component: lazyPage(() => import("./ResetPage.vue")) }),
    createRoute({ getParentRoute: () => root, path: "/verify", component: lazyPage(() => import("./VerifyPage.vue")) }),
    createRoute({ getParentRoute: () => root, path: "/invite", component: lazyPage(() => import("./InvitePage.vue")) }),
  ];
}

/** Everything under /app: a person's organisation, an app's dashboard, the account settings and the deeper views of an environment. */
export function personRoutes(parent: AnyRoute) {
  const signedIn = createRoute({ getParentRoute: () => parent, id: "person", beforeLoad: requirePerson });
  const home = createRoute({ getParentRoute: () => signedIn, path: "/app", component: lazyPage(() => import("./OrgHome.vue")), validateSearch: dashSearch });
  const dashboard = createRoute({
    getParentRoute: () => signedIn,
    path: "/app/apps/$appId",
    component: lazyPage(() => import("./AppDashboard.vue")),
    validateSearch: dashSearch,
  });
  const envSearch = (s: Record<string, unknown>) => (s.env === "sandbox" || s.env === "live" ? { env: s.env } : {});
  const keys = createRoute({ getParentRoute: () => signedIn, path: "/app/apps/$appId/keys", component: lazyPage(() => import("./ApiKeysPage.vue")), validateSearch: envSearch });
  const appSettings = createRoute({ getParentRoute: () => signedIn, path: "/app/apps/$appId/settings", component: lazyPage(() => import("./AppSettingsPage.vue")), validateSearch: envSearch });
  // Going live and the sandbox's own tools (phase 45): the checklist and promote, and reset and scenarios.
  const goLive = createRoute({ getParentRoute: () => signedIn, path: "/app/apps/$appId/go-live", component: lazyPage(() => import("./GoLivePage.vue")), validateSearch: envSearch });
  const sandbox = createRoute({ getParentRoute: () => signedIn, path: "/app/apps/$appId/sandbox", component: lazyPage(() => import("./SandboxPage.vue")), validateSearch: envSearch });
  const members = createRoute({ getParentRoute: () => signedIn, path: "/app/members", component: lazyPage(() => import("./MembersPage.vue")) });
  const settings = createRoute({ getParentRoute: () => signedIn, path: "/app/settings", component: lazyPage(() => import("./SettingsPage.vue")) });
  const tenant = createRoute({ getParentRoute: () => signedIn, path: "/app/t/$id", component: lazyPage(() => import("./ProductTenantLayout.vue")) });
  // Invoices and payments are pages of the product: the console's versions speak operator-only routes (saga, transfers, the raw ledger).
  const invoices = createRoute({ getParentRoute: () => tenant, path: "invoices", component: lazyPage(() => import("./InvoicesPage.vue")) });
  const invoice = createRoute({ getParentRoute: () => tenant, path: "invoices/$invoiceId", component: lazyPage(() => import("./InvoiceDetailPage.vue")) });
  const payments = createRoute({ getParentRoute: () => tenant, path: "payments", component: lazyPage(() => import("./PaymentsPage.vue")) });
  return signedIn.addChildren([
    home,
    dashboard,
    keys,
    appSettings,
    goLive,
    sandbox,
    members,
    settings,
    // One line per page, and the same page in PERSON_PAGES (app/layout/nav.ts), which is what puts it in the sidebar.
    tenant.addChildren([
      ...customerRoutes(tenant),
      ...subscriptionRoutes(tenant),
      ...creditRoutes(tenant),
      ...packageRoutes(tenant),
      ...pricingRoutes(tenant),
      invoices,
      invoice,
      payments,
      ...paymentLinkRoutes(tenant),
      ...accountingRoutes(tenant),
      ...affiliateRoutes(tenant),
      ...reportRoutes(tenant),
      ...webhookRoutes(tenant),
      ...mailRoutes(tenant),
    ]),
  ]);
}
