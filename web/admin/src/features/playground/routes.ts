import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import PlaygroundPage from "./PlaygroundPage.vue";

export function playgroundRoutes(parent: AnyRoute) {
  return [createRoute({ getParentRoute: () => parent, path: "/playground", component: PlaygroundPage })];
}
