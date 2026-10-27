import { computed, ref, toValue, watch, type MaybeRefOrGetter } from "vue";
import { matchesSearch, paginate, PAGE_SIZES } from "./paging";
import { idOf, searchParams, setKind, updateSearch } from "./sheets";

/**
 * The state of a list the console already holds in full: a search text, filters (a key and the values it may equal: a row passes when it matches any of
 * them, no values means no filter), a page and a page size. Changing a search, a filter or the size starts again from the first page. Server-paged lists use DataPagination's token mode instead.
 *
 * The detail sheet of a row walks the whole filtered list, not just the page on screen: `detail` is the absolute index into `filtered` (null is closed),
 * `row` the row it points at, and the table moves to the page that holds it, so previous/next may cross pages. `open(i)` takes the index of a row on
 * the page shown, which is what a table row has.
 *
 * With `url` the filters are kept in the address, each as `?<key>=a,b` (`?status=open,paid`; a comma inside a value is written `%2C`), so a reload, a
 * shared link and "back" bring the same filters; a string is put before every key (two lists on one page, or a key the address already uses: `env`).
 * Only one table of a page should use the bare keys.
 *
 * With `sheet` ({ kind, id }) the open row is written into the address as `?sheet=<kind>:<id>` and the sheet opens by itself when the address names a
 * row (a reload, a shared link, "forward" in the strip of sheets); a filter that hides the named row is cleared.
 */
export function useTableState<T>(rows: MaybeRefOrGetter<readonly T[] | undefined>, opts: { searchFields: (row: T) => (string | number | null | undefined)[]; filters?: Record<string, (row: T, value: string) => boolean>; url?: boolean | string; size?: number; sheet?: { kind: string; id: (row: T) => string | undefined; loaded?: MaybeRefOrGetter<boolean> } }) {
  const search = ref("");
  const filters = ref<Record<string, string[]>>({});
  const page = ref(1);
  const size = ref<number>(opts.size ?? PAGE_SIZES[0]);

  const filtered = computed(() =>
    (toValue(rows) ?? []).filter((r) => {
      if (!matchesSearch(opts.searchFields(r), search.value)) return false;
      return Object.entries(filters.value).every(([k, vs]) => {
        const wanted = vs.filter((v) => v !== "");
        return wanted.length === 0 || wanted.some((v) => opts.filters?.[k]?.(r, v) ?? true);
      });
    }),
  );
  const paged = computed(() => paginate(filtered.value, page.value, size.value));
  watch([search, filters, size], () => (page.value = 1), { deep: true });

  if (opts.url) {
    const prefix = typeof opts.url === "string" ? opts.url : "";
    const keys = Object.keys(opts.filters ?? {});
    const fromAddress = () => Object.fromEntries(keys.map((k) => [k, parseValues(searchParams.value[prefix + k])]).filter(([, v]) => (v as string[]).length > 0)) as Record<string, string[]>;
    const SEP = "\u0000";
    filters.value = fromAddress();
    // The table writes what the person chose; the address writes back what "back" or a pasted link says. An address that keeps nothing of it (a route that
    // drops what it does not know) changes nothing here: only a change of the address counts.
    watch(
      filters,
      (now) => {
        const wanted = Object.fromEntries(keys.map((k) => [prefix + k, writeValues((now[k] ?? []).filter((v) => v !== ""))]));
        if (keys.every((k) => (searchParams.value[prefix + k] === undefined ? undefined : String(searchParams.value[prefix + k])) === wanted[prefix + k])) return;
        updateSearch((prev) => ({ ...prev, ...wanted }));
      },
      { deep: true },
    );
    watch(
      () => keys.map((k) => String(searchParams.value[prefix + k] ?? "")).join(SEP),
      () => {
        const next = fromAddress();
        if (!keys.every((k) => (next[k] ?? []).join(SEP) === (filters.value[k] ?? []).filter((v) => v !== "").join(SEP))) filters.value = next;
      },
    );
  }

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
    const { kind, id, loaded } = opts.sheet;
    // Synchronous: the watcher below closes the sheet when the address names no row, and must not run between a row being opened and the address hearing of it.
    watch(
      detail,
      (d) => {
        const r = d === null ? undefined : filtered.value[d];
        setKind(kind, (r === undefined ? undefined : id(r)) ?? null);
      },
      { flush: "sync" },
    );
    watch([() => idOf(kind), filtered, () => (loaded === undefined ? false : toValue(loaded))], ([named, , isLoaded]) => {
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
      // The list has arrived and no row is the named one (a dead link, a deleted record): the name leaves the address rather than staying there for ever.
      else if (isLoaded && !(toValue(rows) ?? []).some((r) => id(r) === named)) setKind(kind, null);
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
    active: computed(() => search.value.trim() !== "" || Object.values(filters.value).some((vs) => vs.some((v) => v !== ""))),
    clear() {
      search.value = "";
      filters.value = {};
    },
  };
}

/** The values an address parameter holds: comma separated, a comma inside a value written %2C (and a percent %25). Numbers and booleans the router parsed come back as text. */
export function parseValues(raw: unknown): string[] {
  if (raw === undefined || raw === null || raw === "") return [];
  return String(raw)
    .split(",")
    .filter(Boolean)
    .map((v) => v.replaceAll("%2C", ",").replaceAll("%25", "%"));
}

/** The address parameter for some values (undefined takes the parameter off). */
export function writeValues(values: readonly string[]): string | undefined {
  return values.length === 0 ? undefined : values.map((v) => v.replaceAll("%", "%25").replaceAll(",", "%2C")).join(",");
}
