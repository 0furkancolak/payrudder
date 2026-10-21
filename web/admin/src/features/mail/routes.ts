import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function mailRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "mail", component: lazyPage(() => import("./MailPage.vue")) })];
}
