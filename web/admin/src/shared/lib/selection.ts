import { computed, ref, toValue, type MaybeRefOrGetter } from "vue";

/**
 * The ticks of a list: rows are ticked by id, so a refetch does not lose them, and `selectedRows` are only the ticked rows among `matching` (what the search and
 * filters show now): a row that is hidden is never acted on, whatever was ticked before. `onPage` is the page shown, which the header's tick ticks. `useTableState`
 * uses this for lists it pages in the browser; a list the server pages (payment links, partners) passes the page it holds as both.
 */
export function useSelection<T>(matching: MaybeRefOrGetter<readonly T[]>, onPage: MaybeRefOrGetter<readonly T[]>, rowId: ((row: T) => string | undefined) | undefined) {
  const ticked = ref<Set<string>>(new Set());
  const idOfRow = (r: T) => rowId?.(r);
  const idsOf = (rows: readonly T[]) => rows.map(idOfRow).filter((id): id is string => id !== undefined);
  const selectedRows = computed(() => toValue(matching).filter((r) => {
    const id = idOfRow(r);
    return id !== undefined && ticked.value.has(id);
  }));
  const pageIds = computed(() => idsOf(toValue(onPage)));
  const matchingIds = computed(() => idsOf(toValue(matching)));
  const allOnPage = computed(() => pageIds.value.length > 0 && pageIds.value.every((id) => ticked.value.has(id)));
  const someOnPage = computed(() => pageIds.value.some((id) => ticked.value.has(id)));
  const allMatching = computed(() => matchingIds.value.length > 0 && matchingIds.value.every((id) => ticked.value.has(id)));

  return {
    selectedRows,
    allOnPage,
    someOnPage,
    /** Every row the search and filters show, on every page, is ticked. */
    allMatching,
    /** The page shown is ticked, more rows match than it holds, and not all of them are ticked yet. */
    canSelectAll: computed(() => allOnPage.value && matchingIds.value.length > pageIds.value.length && !allMatching.value),
    isTicked: (r: T) => {
      const id = idOfRow(r);
      return id !== undefined && ticked.value.has(id);
    },
    tick(r: T, on: boolean) {
      const id = idOfRow(r);
      if (id === undefined) return;
      const next = new Set(ticked.value);
      if (on) next.add(id);
      else next.delete(id);
      ticked.value = next;
    },
    /** Ticks every row of the page shown, or unticks them when all are ticked. */
    togglePage() {
      const next = new Set(ticked.value);
      const on = !allOnPage.value;
      for (const id of pageIds.value) {
        if (on) next.add(id);
        else next.delete(id);
      }
      ticked.value = next;
    },
    /** Ticks every row the search and filters show, on every page. */
    selectAll() {
      ticked.value = new Set([...ticked.value, ...matchingIds.value]);
    },
    clearSelection() {
      ticked.value = new Set();
    },
  };
}

/** What `useBulk` needs of a list that is paged by the server: the page held is all there is to tick, so its `size` and `total` are its own length. */
export function useServerPageSelection<T>(rows: MaybeRefOrGetter<readonly T[]>, rowId: (row: T) => string | undefined) {
  const sel = useSelection(rows, rows, rowId);
  return { ...sel, size: computed(() => toValue(rows).length), total: computed(() => toValue(rows).length) };
}
