import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function playgroundRoutes(parent: AnyRoute) {
  return [createRoute({ getParentRoute: () => parent, path: "/playground", component: lazyPage(() => import("./PlaygroundPage.vue")) })];
}
