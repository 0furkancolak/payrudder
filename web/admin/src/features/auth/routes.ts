import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import SignInPage from "./SignInPage.vue";

export function authRoutes(parent: AnyRoute) {
  return [createRoute({ getParentRoute: () => parent, path: "/", component: SignInPage })];
}
