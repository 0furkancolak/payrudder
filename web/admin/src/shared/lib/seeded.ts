import { ref, toValue, watch, type MaybeRefOrGetter, type Ref } from "vue";

/**
 * A ref that starts as `initial` and takes the first real value `source` gives (the tenant's currency, which arrives after the page is drawn), once: after
 * that the person owns it, and a later change of the source does not overwrite what they chose.
 */
export function useSeededRef<T>(source: MaybeRefOrGetter<T | "" | null | undefined>, initial: T): Ref<T> {
  const value = ref(initial) as Ref<T>;
  let seeded = false;
  watch(
    () => toValue(source),
    (v) => {
      if (v && !seeded) {
        seeded = true;
        value.value = v as T;
      }
    },
    { immediate: true },
  );
  return value;
}
