import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function connectRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "connect", component: lazyPage(() => import("./ConnectPage.vue")) })];
}
