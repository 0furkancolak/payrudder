import { createRoute, redirect, type AnyRoute } from "@tanstack/vue-router";
import { requireOperator } from "./guard";

/** /operator is the door of the operator console, shown in the sidebar only to operators: it opens the tenant list. */
export function authRoutes(parent: AnyRoute) {
  return [
    createRoute({
      getParentRoute: () => parent,
      path: "/operator",
      beforeLoad: () => {
        requireOperator();
        throw redirect({ to: "/tenants" });
      },
    }),
  ];
}
