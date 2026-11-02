import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

/** A tenant's audit log (under /app/t/{tenant} for a person, under /tenants/{id} for the operator). */
export function auditRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "audit", component: lazyPage(() => import("./AuditPage.vue")) })];
}

/** The operator's log across tenants. */
export function auditOperatorRoutes(parent: AnyRoute) {
  return [createRoute({ getParentRoute: () => parent, path: "/audit", component: lazyPage(() => import("./AuditAdminPage.vue")) })];
}
