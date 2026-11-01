import { inject, provide, ref, type InjectionKey, type Ref } from "vue";

/** A filter that is on, as the toolbar lists it where the filters themselves are out of sight (a phone keeps them behind one button). */
export interface AppliedFilter {
  id: number;
  label: string;
  text: string;
  clear: () => void;
}

const KEY: InjectionKey<Ref<AppliedFilter[]>> = Symbol("appliedFilters");
let nextId = 1;
export const newFilterId = () => nextId++;

/** Called by the toolbar: the filters placed in it report themselves here (see `useAppliedReport`). */
export function provideApplied(): Ref<AppliedFilter[]> {
  const list = ref<AppliedFilter[]>([]);
  provide(KEY, list);
  return list;
}

/** Called by a filter: puts itself in the toolbar's list while it is on (`entry` null) and takes itself off when it is not; a filter outside a toolbar reports to nobody. */
export function useAppliedReport() {
  const list = inject(KEY, null);
  return {
    report(id: number, entry: Omit<AppliedFilter, "id"> | null) {
      if (!list) return;
      const rest = list.value.filter((a) => a.id !== id);
      list.value = entry === null ? rest : [...rest, { id, ...entry }];
    },
  };
}
