import { createRoute, redirect, type AnyRoute, type RouteComponent } from "@tanstack/vue-router";
import { defineAsyncComponent } from "vue";
import { accountingRoutes } from "@/features/accounting/routes";
import { creditRoutes } from "@/features/credits/routes";
import { customerRoutes } from "@/features/customers/routes";
import { mailRoutes } from "@/features/mail/routes";
import { packageRoutes } from "@/features/packages/routes";
import { paymentLinkRoutes } from "@/features/paymentlinks/routes";
import { pricingRoutes } from "@/features/pricing/routes";
import { reportRoutes } from "@/features/reports/routes";
import { subscriptionRoutes } from "@/features/subscriptions/routes";
import { webhookRoutes } from "@/features/webhooks/routes";
import { isOperator, isPerson } from "@/shared/lib/session";

// The product's pages are loaded when first visited, so the public pages stay small. The deeper views reuse the console's own route factories.
const lazy = (load: () => Promise<{ default: object }>): RouteComponent => defineAsyncComponent(load as never) as unknown as RouteComponent;

/** beforeLoad for the pages that need a signed-in person. */
export function requirePerson(): void {
  if (!isPerson.value) throw redirect({ to: "/signin" });
}

/** beforeLoad for the sign-in and sign-up pages and for "/": somebody who is signed in goes to their home. */
export function redirectHome(): void {
  if (isPerson.value) throw redirect({ to: "/app" });
  if (isOperator.value) throw redirect({ to: "/tenants" });
}

/** The public pages: sign in, sign up, forgot and reset password, verify e-mail (and "/", which is the sign-in page for a visitor and a redirect for somebody signed in). */
export function publicProductRoutes(root: AnyRoute) {
  const signIn = lazy(() => import("./SignInPage.vue"));
  const signUp = lazy(() => import("./SignUpPage.vue"));
  const validateSignIn = (s: Record<string, unknown>) => (s.expired === "1" ? { expired: "1" } : {});
  return [
    createRoute({ getParentRoute: () => root, path: "/", beforeLoad: redirectHome, component: signIn, validateSearch: validateSignIn }),
    createRoute({ getParentRoute: () => root, path: "/signin", beforeLoad: redirectHome, component: signIn, validateSearch: validateSignIn }),
    createRoute({ getParentRoute: () => root, path: "/signup", beforeLoad: redirectHome, component: signUp }),
    createRoute({ getParentRoute: () => root, path: "/forgot", beforeLoad: redirectHome, component: lazy(() => import("./ForgotPage.vue")) }),
    // The pages the mails open work signed in or out: the token in the link is the credential, and a person may follow it from any browser.
    createRoute({ getParentRoute: () => root, path: "/reset", component: lazy(() => import("./ResetPage.vue")) }),
    createRoute({ getParentRoute: () => root, path: "/verify", component: lazy(() => import("./VerifyPage.vue")) }),
    createRoute({ getParentRoute: () => root, path: "/invite", component: lazy(() => import("./InvitePage.vue")) }),
  ];
}

/** Everything under /app: a person's organisation, an app's dashboard, the account settings and the deeper views of an environment. */
export function personRoutes(parent: AnyRoute) {
  const signedIn = createRoute({ getParentRoute: () => parent, id: "person", beforeLoad: requirePerson });
  const home = createRoute({ getParentRoute: () => signedIn, path: "/app", component: lazy(() => import("./OrgHome.vue")) });
  const dashboard = createRoute({
    getParentRoute: () => signedIn,
    path: "/app/apps/$appId",
    component: lazy(() => import("./AppDashboard.vue")),
    validateSearch: (s: Record<string, unknown>) => (s.env === "sandbox" ? { env: "sandbox" } : {}),
  });
  const envSearch = (s: Record<string, unknown>) => (s.env === "sandbox" ? { env: "sandbox" } : {});
  const keys = createRoute({ getParentRoute: () => signedIn, path: "/app/apps/$appId/keys", component: lazy(() => import("./ApiKeysPage.vue")), validateSearch: envSearch });
  const appSettings = createRoute({ getParentRoute: () => signedIn, path: "/app/apps/$appId/settings", component: lazy(() => import("./AppSettingsPage.vue")), validateSearch: envSearch });
  const settings = createRoute({ getParentRoute: () => signedIn, path: "/app/settings", component: lazy(() => import("./SettingsPage.vue")) });
  const tenant = createRoute({ getParentRoute: () => signedIn, path: "/app/t/$id", component: lazy(() => import("./ProductTenantLayout.vue")) });
  return signedIn.addChildren([
    home,
    dashboard,
    keys,
    appSettings,
    settings,
    // One line per page, and the same page in PERSON_PAGES (app/layout/nav.ts), which is what puts it in the sidebar.
    tenant.addChildren([
      ...customerRoutes(tenant),
      ...subscriptionRoutes(tenant),
      ...creditRoutes(tenant),
      ...packageRoutes(tenant),
      ...pricingRoutes(tenant),
      ...paymentLinkRoutes(tenant),
      ...accountingRoutes(tenant),
      ...reportRoutes(tenant),
      ...webhookRoutes(tenant),
      ...mailRoutes(tenant),
    ]),
  ]);
}
