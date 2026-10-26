import { useSearch } from "@tanstack/vue-router";
import { toValue, watch, type MaybeRefOrGetter } from "vue";
import { updateSearch } from "./sheets";

/**
 * A link from another page names the record whose sheet it wants open as a query parameter of the list (`?invoice=<id>`). Once the list is `ready` (its rows
 * are loaded) `open` is called with the id and the parameter is taken off the address, so closing the sheet and going back does not open it again.
 */
export function useOpenParam(key: string, ready: MaybeRefOrGetter<boolean>, open: (id: string) => void) {
  const search = useSearch({ strict: false });
  watch(
    [() => (search.value as Record<string, unknown>)[key], () => toValue(ready)],
    ([id, ok]) => {
      if (id === undefined || id === null || id === "" || !ok) return;
      open(String(id));
      updateSearch((prev) => ({ ...prev, [key]: undefined }));
    },
    { immediate: true },
  );
}
