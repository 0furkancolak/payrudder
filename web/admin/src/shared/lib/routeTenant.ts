import { useParams } from "@tanstack/vue-router";
import { computed } from "vue";

/** The tenant of a page under `/app/t/$id/...`: the `id` of the route, empty on a page that has none. */
export function useRouteTenantId() {
  const params = useParams({ strict: false });
  return computed(() => String(params.value.id ?? ""));
}
