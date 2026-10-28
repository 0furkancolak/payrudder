import { createRoute, type AnyRoute } from "@tanstack/vue-router";
import { lazyPage } from "@/shared/lib/lazy";
import { sheetSearch } from "@/features/dashboard/lib";

/** The person's help page (organisation level, with the sheet of a ticket in the address). */
export function supportRoutes(parent: AnyRoute) {
  return [createRoute({ getParentRoute: () => parent, path: "/app/support", component: lazyPage(() => import("./SupportPage.vue")), validateSearch: (s: Record<string, unknown>) => ({ ...sheetSearch(s) }) })];
}

/** The operator's inbox. */
export function supportInboxRoutes(parent: AnyRoute) {
  return [createRoute({ getParentRoute: () => parent, path: "/support", component: lazyPage(() => import("./SupportInboxPage.vue")), validateSearch: (s: Record<string, unknown>) => ({ ...sheetSearch(s) }) })];
}
