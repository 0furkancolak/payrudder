import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import CreditsPage from "./CreditsPage.vue";

export function creditRoutes(tenant: AnyRoute) {
  return [createRoute({ getParentRoute: () => tenant, path: "credits", component: CreditsPage })];
}
