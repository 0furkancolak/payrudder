import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function appRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "apps", component: lazyPage(() => import("./AppsPage.vue")) })];
}
