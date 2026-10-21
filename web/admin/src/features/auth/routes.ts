import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";

/** The operator's token page. People sign in at "/" (features/product); the operator's console keeps its own door. */
export function authRoutes(parent: AnyRoute) {
  return [createRoute({ getParentRoute: () => parent, path: "/operator", component: lazyPage(() => import("./SignInPage.vue")) })];
}
