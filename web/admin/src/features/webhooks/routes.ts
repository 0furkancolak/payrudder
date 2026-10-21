import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function webhookRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "webhooks", component: lazyPage(() => import("./WebhooksPage.vue")) })];
}
