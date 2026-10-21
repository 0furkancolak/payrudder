import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function creditRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "credits", component: lazyPage(() => import("./CreditsPage.vue")) })];
}
