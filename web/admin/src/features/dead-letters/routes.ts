import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function deadLetterRoutes(parent: AnyRoute) {
  return [createRoute({ getParentRoute: () => parent, path: "/dead-letters", component: lazyPage(() => import("./DeadLettersPage.vue")) })];
}
