import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

/** An app's blocklist (under /app/t/{tenant} for a person, under /tenants/{id} for the operator). */
export function blocklistRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "blocklist", component: lazyPage(() => import("./BlocklistPage.vue")) })];
}

/** The operator's platform-wide list. */
export function blocklistOperatorRoutes(parent: AnyRoute) {
  return [createRoute({ getParentRoute: () => parent, path: "/blocklist", component: lazyPage(() => import("./PlatformBlocklistPage.vue")) })];
}
