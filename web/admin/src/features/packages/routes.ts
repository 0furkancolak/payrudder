import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function packageRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "packages", component: lazyPage(() => import("./PackagesPage.vue")) })];
}
