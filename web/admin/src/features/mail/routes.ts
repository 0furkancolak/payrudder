import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import MailPage from "./MailPage.vue";

export function mailRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "mail", component: MailPage })];
}
