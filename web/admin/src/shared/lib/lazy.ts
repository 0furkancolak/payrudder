import type { RouteComponent } from "@tanstack/vue-router";
import { defineAsyncComponent } from "vue";

/**
 * A page that is fetched when its route is first visited, so the first load carries the frame and the sign-in page, not the whole console.
 *
 * `suspensible: false` matters. The router renders its matches inside a `<Suspense>`, and an async component is suspensible by default: it then registers with
 * that Suspense and mounts itself into the place it was first rendered at when its code arrives. A person who leaves the page before that (the next page is
 * a click away from the first paint) has made that place disappear, and Vue throws `insertBefore` of null from inside the router's own match component, which
 * the router shows as its "Invariant failed" error page. Not suspensible, the page is an ordinary component that renders nothing until its code is there and
 * is simply dropped when its route is left, with nothing for the Suspense to resolve late.
 */
export function lazyPage(load: () => Promise<{ default: object }>): RouteComponent {
  return defineAsyncComponent({ loader: load as never, suspensible: false }) as unknown as RouteComponent;
}
