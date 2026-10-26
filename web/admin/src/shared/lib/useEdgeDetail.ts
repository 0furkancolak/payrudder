import { computed, ref, toValue, watch, type MaybeRefOrGetter } from "vue";

/**
 * The detail sheet of a list the console holds one server page of at a time. `index` counts over the loaded `rows`; DetailSheet is given `edge` and
 * emits `edge` ("prev" | "next") when the reader steps past the loaded rows, which `onEdge` answers by loading that page and putting the index on its
 * near row (the first one going forward, the last one going back) as soon as the page's rows arrive.
 */
export function useEdgeDetail<T>(rows: MaybeRefOrGetter<readonly T[]>, pages: { hasPrev: MaybeRefOrGetter<boolean>; hasNext: MaybeRefOrGetter<boolean>; prev: () => unknown; next: () => unknown }) {
  const index = ref<number | null>(null);
  const landing = ref<"first" | "last" | null>(null);
  const row = computed(() => (index.value === null ? undefined : toValue(rows)[index.value]));
  // Waiting for a page is not a moment to step again.
  const edge = computed(() => ({ prev: !landing.value && toValue(pages.hasPrev), next: !landing.value && toValue(pages.hasNext) }));
  watch(
    () => toValue(rows),
    (now) => {
      if (!landing.value || now.length === 0) return;
      index.value = landing.value === "first" ? 0 : now.length - 1;
      landing.value = null;
    },
  );
  return {
    index,
    row,
    edge,
    onEdge(dir: "prev" | "next") {
      if (landing.value || index.value === null) return;
      landing.value = dir === "next" ? "first" : "last";
      void (dir === "next" ? pages.next() : pages.prev());
    },
  };
}
