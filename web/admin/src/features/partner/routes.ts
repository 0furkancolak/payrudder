import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

/** The partner portal: session routes under /app (no tenant, no API key). The fixed path "apply" is matched before the affiliation id. */
export function partnerRoutes(signedIn: AnyRoute) {
  const validateApply = (s: Record<string, unknown>) => (typeof s.tenant === "string" ? { tenant: s.tenant } : {});
  return [
    createRoute({ getParentRoute: () => signedIn, path: "/app/partner", component: lazyPage(() => import("./PartnerHome.vue")) }),
    createRoute({ getParentRoute: () => signedIn, path: "/app/partner/apply", component: lazyPage(() => import("./ApplyPage.vue")), validateSearch: validateApply }),
    createRoute({ getParentRoute: () => signedIn, path: "/app/partner/$id", component: lazyPage(() => import("./PartnerDetail.vue")) }),
  ];
}
