import { computed, ref, toValue, watch, type MaybeRefOrGetter } from "vue";
import { matchesSearch, paginate, PAGE_SIZES } from "./paging";
import { idOf, setKind } from "./sheets";

/**
 * The state of a list the console already holds in full: a search text, filters (a key and the value it must equal; empty means no filter), a page and
 * a page size. Changing a search, a filter or the size starts again from the first page. Server-paged lists use DataPagination's token mode instead.
 *
 * The detail sheet of a row walks the whole filtered list, not just the page on screen: `detail` is the absolute index into `filtered` (null is closed),
 * `row` the row it points at, and the table moves to the page that holds it, so previous/next may cross pages. `open(i)` takes the index of a row on
 * the page shown, which is what a table row has.
 *
 * With `sheet` ({ kind, id }) the open row is written into the address as `?sheet=<kind>:<id>` and the sheet opens by itself when the address names a
 * row (a reload, a shared link, "forward" in the strip of sheets); a filter that hides the named row is cleared.
 */
export function useTableState<T>(rows: MaybeRefOrGetter<readonly T[] | undefined>, opts: { searchFields: (row: T) => (string | number | null | undefined)[]; filters?: Record<string, (row: T, value: string) => boolean>; size?: number; sheet?: { kind: string; id: (row: T) => string | undefined } }) {
  const search = ref("");
  const filters = ref<Record<string, string>>({});
  const page = ref(1);
  const size = ref<number>(opts.size ?? PAGE_SIZES[0]);

  const filtered = computed(() =>
    (toValue(rows) ?? []).filter((r) => {
      if (!matchesSearch(opts.searchFields(r), search.value)) return false;
      return Object.entries(filters.value).every(([k, v]) => v === "" || (opts.filters?.[k]?.(r, v) ?? true));
    }),
  );
  const paged = computed(() => paginate(filtered.value, page.value, size.value));
  watch([search, filters, size], () => (page.value = 1), { deep: true });

  const detail = ref<number | null>(null);
  const row = computed(() => (detail.value === null ? undefined : filtered.value[detail.value]));
  // The table follows the sheet to the page of the row it shows.
  watch(detail, (d) => {
    if (d !== null) page.value = Math.floor(d / size.value) + 1;
  });
  // A list that shrinks under an open sheet (rows gone after a refetch) closes it rather than showing a row that is not there.
  watch(filtered, (rows) => {
    if (detail.value !== null && detail.value >= rows.length) detail.value = null;
  });

  if (opts.sheet) {
    const { kind, id } = opts.sheet;
    watch(detail, (d) => {
      const r = d === null ? undefined : filtered.value[d];
      setKind(kind, (r === undefined ? undefined : id(r)) ?? null);
    });
    watch([() => idOf(kind), filtered], ([named]) => {
      if (named === undefined) {
        if (detail.value !== null) detail.value = null;
        return;
      }
      const shown = row.value;
      if (shown !== undefined && id(shown) === named) return;
      const find = () => filtered.value.findIndex((r) => id(r) === named);
      let at = find();
      // A filter hides the row the address names: the address wins.
      if (at < 0 && (toValue(rows) ?? []).some((r) => id(r) === named)) {
        search.value = "";
        filters.value = {};
        at = find();
      }
      if (at >= 0) detail.value = at;
    });
  }

  return {
    search,
    filters,
    page,
    size,
    filtered,
    items: computed(() => paged.value.items),
    pages: computed(() => paged.value.pages),
    current: computed(() => paged.value.page),
    total: computed(() => filtered.value.length),
    detail,
    row,
    /** Opens the sheet on a row of the page shown (its index in `items`). */
    open(i: number) {
      detail.value = (paged.value.page - 1) * size.value + i;
    },
    /** Opens the sheet on the first row of the whole filtered list that satisfies the test; false when none does (filters may hide it). */
    openWhere(test: (row: T) => boolean) {
      const at = filtered.value.findIndex(test);
      if (at >= 0) detail.value = at;
      return at >= 0;
    },
    /** Whether the row at this index of the page shown is the one the sheet shows. */
    isOpen: (i: number) => detail.value === (paged.value.page - 1) * size.value + i,
    active: computed(() => search.value.trim() !== "" || Object.values(filters.value).some((v) => v !== "")),
    clear() {
      search.value = "";
      filters.value = {};
    },
  };
}
