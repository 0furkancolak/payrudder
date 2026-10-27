import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

export function playgroundRoutes(parent: AnyRoute) {
  return [
    createRoute({ getParentRoute: () => parent, path: "/playground", component: lazyPage(() => import("./PlaygroundPage.vue")) }),
    // The shared controls to try and to test; not in the menu.
    createRoute({ getParentRoute: () => parent, path: "/playground/components", component: lazyPage(() => import("./ComponentsPage.vue")) }),
  ];
}
