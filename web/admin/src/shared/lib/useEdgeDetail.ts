import { computed, ref, toValue, watch, type MaybeRefOrGetter } from "vue";
import { idOf, setKind, sheetKey } from "./sheets";

/** How many pages the address's sheet is looked for beyond the first one loaded before it is given up. */
export const SEEK_PAGES = 10;

/**
 * The detail sheet of a list the console holds one server page of at a time. `index` counts over the loaded `rows`; DetailSheet is given `edge` and
 * emits `edge` ("prev" | "next") when the reader steps past the loaded rows, which `onEdge` answers by loading that page and putting the index on its
 * near row (the first one going forward, the last one going back) as soon as the page's rows arrive.
 *
 * With `sheet` the open row is in the address as `<kind>:<id>` like the sheet of any row (see shared/lib/sheets), so a reload or a shared link opens it:
 * the row is looked for on the page loaded, then (when `missing` has nothing to change, e.g. a filter that hides the row) on the pages after it, up to
 * SEEK_PAGES of them, and a row that is not found is taken off the address. The search starts when the list is `ready` (by default: has rows). `missing` returns true when it changed what is listed or opened the row
 * another way, and the search goes on with what is listed then. A search that gives up calls `rewind`, which takes the list back to its first page.
 */
export function useEdgeDetail<T>(
  rows: MaybeRefOrGetter<readonly T[]>,
  pages: { hasPrev: MaybeRefOrGetter<boolean>; hasNext: MaybeRefOrGetter<boolean>; prev: () => unknown; next: () => unknown },
  sheet?: { kind: string; id: (row: T) => string; ready?: MaybeRefOrGetter<boolean>; missing?: (id: string) => boolean; rewind?: () => unknown },
) {
  const index = ref<number | null>(null);
  const landing = ref<"first" | "last" | null>(null);
  let leaving: readonly T[] = [];
  const row = computed(() => (index.value === null ? undefined : toValue(rows)[index.value]));
  // Waiting for a page is not a moment to step again.
  const edge = computed(() => ({ prev: !landing.value && toValue(pages.hasPrev), next: !landing.value && toValue(pages.hasNext) }));
  watch(
    () => toValue(rows),
    (now) => {
      if (!landing.value || now.length === 0) return;
      // The same rows again (the pages loaded grew, the one on screen did not): the page asked for has not come yet.
      if (now.length === leaving.length && now.every((r, i) => r === leaving[i])) return;
      index.value = landing.value === "first" ? 0 : now.length - 1;
      landing.value = null;
    },
  );
  if (sheet) {
    // Synchronous: the watcher below closes the sheet when the address names no row, and must not run between a row being opened and the address hearing of it.
    // The name this list wrote last: while the rows are being swapped it is not in them, which is not the address asking for another row.
    let written: string | undefined;
    // A page being loaded leaves the index where it is with no row, which keeps the name.
    watch(
      () => (row.value === undefined ? undefined : sheet.id(row.value)),
      (id) => {
        if (id === undefined) return;
        written = sheetKey(id);
        setKind(sheet.kind, written);
      },
      { flush: "sync" },
    );
    watch(
      index,
      (i) => {
        if (i !== null) return;
        written = undefined;
        setKind(sheet.kind, null);
      },
      { flush: "sync" },
    );
    let sought = 0;
    let waitingOn: readonly T[] | undefined;
    watch(
      [() => idOf(sheet.kind), () => toValue(rows), () => (sheet.ready === undefined ? toValue(rows).length > 0 : toValue(sheet.ready))],
      ([named, now, ready]) => {
        if (named === undefined) {
          sought = 0;
          if (index.value !== null && !landing.value) index.value = null;
          return;
        }
        // A page on its way: the rows it replaces are not looked at again.
        if (landing.value || !ready || now === waitingOn || named === written) return;
        const id = decodeURIComponent(named);
        const at = now.findIndex((r) => sheet.id(r) === id);
        if (at >= 0) {
          sought = 0;
          if (index.value !== at) index.value = at;
          return;
        }
        if (sheet.missing?.(id)) return;
        if (sought < SEEK_PAGES && toValue(pages.hasNext)) {
          sought++;
          waitingOn = now;
          void pages.next();
          return;
        }
        // Given up: the reader is back on the first page rather than left on the last one looked at.
        if (sought > 0) void sheet.rewind?.();
        sought = 0;
        setKind(sheet.kind, null);
      },
      { immediate: true },
    );
  }
  return {
    index,
    row,
    edge,
    onEdge(dir: "prev" | "next") {
      if (landing.value || index.value === null) return;
      leaving = toValue(rows);
      landing.value = dir === "next" ? "first" : "last";
      void (dir === "next" ? pages.next() : pages.prev());
    },
  };
}
