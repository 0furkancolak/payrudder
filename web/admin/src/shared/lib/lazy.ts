import type { RouteComponent } from "@tanstack/vue-router";
import { defineAsyncComponent } from "vue";

/** A page that is fetched when its route is first visited, so the first load carries the frame and the sign-in page, not the whole console. */
export function lazyPage(load: () => Promise<{ default: object }>): RouteComponent {
  return defineAsyncComponent(load as never) as unknown as RouteComponent;
}
