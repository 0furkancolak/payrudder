import { computed, ref, toValue, watch, type MaybeRefOrGetter } from "vue";
import { matchesSearch, paginate, PAGE_SIZES } from "./paging";

/**
 * The state of a list the console already holds in full: a search text, filters (a key and the value it must equal; empty means no filter), a page and
 * a page size. Changing a search, a filter or the size starts again from the first page. Server-paged lists use DataPagination's token mode instead.
 */
export function useTableState<T>(rows: MaybeRefOrGetter<readonly T[] | undefined>, opts: { searchFields: (row: T) => (string | number | null | undefined)[]; filters?: Record<string, (row: T, value: string) => boolean>; size?: number }) {
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
    active: computed(() => search.value.trim() !== "" || Object.values(filters.value).some((v) => v !== "")),
    clear() {
      search.value = "";
      filters.value = {};
    },
  };
}
