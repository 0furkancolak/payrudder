import { createRootRoute, createRoute, createRouter } from "@tanstack/vue-router";
import { authRoutes } from "@/features/auth/routes";
import { requireOperator } from "@/features/auth";
import { deadLetterRoutes } from "@/features/dead-letters/routes";
import { playgroundRoutes } from "@/features/playground/routes";
import { tenantRoutes } from "@/features/tenants/routes";
import RootLayout from "./layout/RootLayout.vue";

// Code-based routes: every feature owns its routes and the app only composes them. Everything but the sign-in page sits under
// one pathless route whose beforeLoad turns a visitor without an operator token away.
const root = createRootRoute({ component: RootLayout });
const signedIn = createRoute({ getParentRoute: () => root, id: "signed-in", beforeLoad: requireOperator });

const routeTree = root.addChildren([
  ...authRoutes(root),
  signedIn.addChildren([...tenantRoutes(signedIn), ...playgroundRoutes(signedIn), ...deadLetterRoutes(signedIn)]),
]);

// The router is deliberately not registered with `declare module "@tanstack/vue-router"`: registering it makes every page's types depend on the
// router's, and the router's on the pages (routes import pages, pages use Link and useNavigate), a cycle TypeScript cannot resolve. Route paths
// stay plain strings, checked by the Playwright tests instead.
export const router = createRouter({ routeTree, defaultPreload: "intent" });
