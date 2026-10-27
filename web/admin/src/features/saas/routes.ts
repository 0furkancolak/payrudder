import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

/** The operator's page of the organisations with their plans (mounted under the operator layout). */
export function saasOperatorRoutes(parent: AnyRoute) {
  return [createRoute({ getParentRoute: () => parent, path: "/organisations", component: lazyPage(() => import("./OrganizationsPage.vue")) })];
}
