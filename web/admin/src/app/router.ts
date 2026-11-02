import { returnTo } from "@/features/product/lib";
import { createRootRoute, createRoute, createRouter, retainSearchParams } from "@tanstack/vue-router";
import { authRoutes } from "@/features/auth/routes";
import { requireOperator } from "@/features/auth";
import { deadLetterRoutes } from "@/features/dead-letters/routes";
import { auditOperatorRoutes } from "@/features/audit/routes";
import { blocklistOperatorRoutes } from "@/features/blocklist/routes";
import { supportInboxRoutes } from "@/features/support/routes";
import { playgroundRoutes } from "@/features/playground/routes";
import { productKeys } from "@/features/product/api";
import { personRoutes, publicProductRoutes } from "@/features/product/routes";
import { setupRedirect } from "@/features/product/twoFactor";
import { saasOperatorRoutes } from "@/features/saas/routes";
import { tenantRoutes } from "@/features/tenants/routes";
import { queryClient } from "./query";
import { onSessionLost, onSetupRequired } from "@/shared/lib/session";
import NotFound from "./errors/NotFound.vue";
import RouteError from "./errors/RouteError.vue";
import RootLayout from "./layout/RootLayout.vue";

// Code-based routes: every feature owns its routes and the app only composes them. Three groups:
//  - public: sign in (password, e-mailed code, Google) and sign up for people ("/"); there is no sign-in of the operator's own;
//  - the person's product under /app, behind a session;
//  - the operator's console (/operator, /tenants, /playground, /dead-letters), behind the session of a person the server recognises as an operator.
// The scope (`?app=<id|all>`) is kept by every link inside the app: a navigation that does not name an app keeps the one in the address.
const root = createRootRoute({ component: RootLayout, search: { middlewares: [retainSearchParams(["app"] as never)] } } as never);
const operator = createRoute({ getParentRoute: () => root, id: "signed-in", beforeLoad: (ctx) => requireOperator(ctx) });

const routeTree = root.addChildren([
  ...publicProductRoutes(root),
  ...authRoutes(root),
  personRoutes(root),
  operator.addChildren([...tenantRoutes(operator), ...playgroundRoutes(operator), ...deadLetterRoutes(operator), ...supportInboxRoutes(operator), ...auditOperatorRoutes(operator), ...blocklistOperatorRoutes(operator), ...saasOperatorRoutes(operator)]),
]);

// The router is deliberately not registered with `declare module "@tanstack/vue-router"`: registering it makes every page's types depend on the
// router's, and the router's on the pages (routes import pages, pages use Link and useNavigate), a cycle TypeScript cannot resolve. Route paths
// stay plain strings, checked by the Playwright tests instead.
export const router = createRouter({ routeTree, defaultPreload: "intent", defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFound });

// The gateway refused the person's session (expired, revoked): everything cached belongs to nobody now, and the sign-in page says why.
onSessionLost(() => {
  queryClient.clear();
  const next = returnTo(router.state.location.href);
  void router.navigate({ to: "/signin", search: { expired: "1", app: undefined, ...(next ? { next } : {}) } as never });
});

// An organisation the person owns requires two-factor sign-in and they have none, and the gateway refused a request for it: the profile is read again (its
// flag keeps them on the security page, see RootLayout) and the security page opens now.
onSetupRequired(() => {
  void queryClient.invalidateQueries({ queryKey: productKeys.me });
  // Not from the security page itself, nor from the pages a mailed link opens.
  const to = setupRedirect(router.state.location.pathname, true);
  if (to) void router.navigate({ to, replace: true } as never);
});
