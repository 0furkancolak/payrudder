import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import WebhooksPage from "./WebhooksPage.vue";

export function webhookRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "webhooks", component: WebhooksPage })];
}
