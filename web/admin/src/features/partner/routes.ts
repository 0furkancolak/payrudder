import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

/** The partner portal: session routes under /app (no tenant, no API key). An affiliation opens in a sheet of the portal's home (`?open=<id>`), not on a page of its own. */
export function partnerRoutes(signedIn: AnyRoute) {
  const validateApply = (s: Record<string, unknown>) => (typeof s.tenant === "string" ? { tenant: s.tenant } : {});
  return [
    createRoute({ getParentRoute: () => signedIn, path: "/app/partner", component: lazyPage(() => import("./PartnerHome.vue")) }),
    createRoute({ getParentRoute: () => signedIn, path: "/app/partner/apply", component: lazyPage(() => import("./ApplyPage.vue")), validateSearch: validateApply }),
  ];
}
