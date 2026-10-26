import { useRouter, useSearch } from "@tanstack/vue-router";
import { toValue, watch, type MaybeRefOrGetter } from "vue";

/**
 * A link from another page names the record whose sheet it wants open as a query parameter of the list (`?invoice=<id>`). Once the list is `ready` (its rows
 * are loaded) `open` is called with the id and the parameter is taken off the address, so closing the sheet and going back does not open it again.
 */
export function useOpenParam(key: string, ready: MaybeRefOrGetter<boolean>, open: (id: string) => void) {
  const search = useSearch({ strict: false });
  const router = useRouter();
  watch(
    [() => (search.value as Record<string, unknown>)[key], () => toValue(ready)],
    ([id, ok]) => {
      if (id === undefined || id === null || id === "" || !ok) return;
      open(String(id));
      void router.navigate({ search: ((prev: Record<string, unknown>) => ({ ...prev, [key]: undefined })) as never, replace: true });
    },
    { immediate: true },
  );
}
