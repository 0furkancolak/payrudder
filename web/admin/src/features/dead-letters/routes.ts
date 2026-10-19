import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import DeadLettersPage from "./DeadLettersPage.vue";

export function deadLetterRoutes(parent: AnyRoute) {
  return [createRoute({ getParentRoute: () => parent, path: "/dead-letters", component: DeadLettersPage })];
}
