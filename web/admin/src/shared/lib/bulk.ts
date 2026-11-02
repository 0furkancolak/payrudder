import { until } from "@vueuse/core";
import { computed, ref, type Ref } from "vue";
import { useI18n } from "vue-i18n";
import { askConfirm, type ConfirmOptions } from "./confirm";

/**
 * Runs one action on each ticked row, one after the other, and says what happened to every one: a failure on a row does not stop the rest. The caller shows
 * `failed` (row and error) so the person knows which rows were not changed.
 */
export interface BulkResult<T> {
  ok: T[];
  failed: { row: T; error: unknown }[];
}

export async function runBulk<T>(rows: readonly T[], act: (row: T) => Promise<unknown>): Promise<BulkResult<T>> {
  const out: BulkResult<T> = { ok: [], failed: [] };
  for (const row of rows) {
    try {
      await act(row);
      out.ok.push(row);
    } catch (error) {
      out.failed.push({ row, error });
    }
  }
  return out;
}

/** What `useBulk` needs of a list (useTableState has all of it). */
export interface BulkTable<T> {
  selectedRows: Readonly<Ref<readonly T[]>>;
  size: Readonly<Ref<number>>;
  total: Readonly<Ref<number>>;
  allOnPage: Readonly<Ref<boolean>>;
  canSelectAll: Readonly<Ref<boolean>>;
  tick(row: T, on: boolean): void;
  selectAll(): void;
}

/** A list the console holds only the first server page of: whether the server has more, and how to fetch the rest through the list API's own page tokens. */
export interface RestOfList {
  has: () => boolean;
  load: () => Promise<unknown>;
}

/**
 * Every row after the first server page of a list, by following the list API's own page tokens (`first` is the token the first page ended with). A token that
 * comes back twice or more than `maxPages` pages is an API gone wrong and stops the walk with an error rather than looping.
 */
export async function restOfPages<T>(first: string, fetchPage: (token: string) => Promise<{ rows: readonly T[]; next: string }>, maxPages = 500): Promise<T[]> {
  const rows: T[] = [];
  const seen = new Set<string>();
  for (let token = first; token !== ""; ) {
    if (seen.has(token) || seen.size >= maxPages) throw new Error("the list API does not end");
    seen.add(token);
    const page = await fetchPage(token);
    rows.push(...page.rows);
    token = page.next;
  }
  return rows;
}

/**
 * "Select all N results": ticks every row the search and filters show, on every page. A list that holds only the first server page (`rest`) first fetches the
 * others, waits until the list shows them, and only then ticks, so the selection covers what the filters match on the server's side too.
 */
export async function selectAllResults(selectAll: () => void, rest?: RestOfList): Promise<void> {
  if (rest?.has()) {
    await rest.load();
    await until(() => !rest.has()).toBe(true, { timeout: 10_000, throwOnTimeout: true });
  }
  selectAll();
}

/**
 * The question before a bulk action. An action on more rows than one page holds is always asked about, with the count, because the person sees only a page of
 * them; an action that asks anyway (it cannot be undone) keeps its own question and gets the count added. `ours` are the words for the added part.
 */
export function bulkQuestion(own: ConfirmOptions | undefined, n: number, pageSize: number, ours: { title: string; count: string }): ConfirmOptions | undefined {
  if (n <= pageSize) return own;
  return { ...own, title: own?.title ?? ours.title, body: [own?.body, ours.count].filter(Boolean).join(" "), destructive: own ? own.destructive : false };
}

/**
 * The bulk action of a list, the same everywhere: ask once when the action cannot be undone (or touches more than a page of rows, with the count), run it on the
 * ticked rows one by one, say "n done, m not changed" with the names of the rows that failed, untick the ones that were done and leave the failed ones ticked so
 * the same action can be repeated. `table` is the list's useTableState, `name` is what a row is called in the note, `rest` is set for a list that holds only the first
 * server page. `bar` is what the page binds to BulkBar.
 */
export function useBulk<T>(table: BulkTable<T>, opts: { name: (row: T) => string; rest?: RestOfList }) {
  const { t } = useI18n();
  const note = ref("");
  const busy = ref(false);
  const loading = ref(false);
  /** `rows` narrows the ticked ones the action applies to (revoking skips the keys that are revoked already); `confirm` is the question, when there is one (it may depend on the rows). */
  async function run(act: (row: T) => Promise<unknown>, o: { confirm?: ConfirmOptions | ((rows: readonly T[]) => ConfirmOptions | undefined); rows?: (all: readonly T[]) => readonly T[] } = {}) {
    const rows = o.rows ? o.rows(table.selectedRows.value) : table.selectedRows.value;
    if (rows.length === 0 || busy.value) return;
    const ask = bulkQuestion(typeof o.confirm === "function" ? o.confirm(rows) : o.confirm, rows.length, table.size.value, {
      title: t("common.bulk.confirmAllTitle", { n: rows.length }),
      count: t("common.bulk.confirmAll", { n: rows.length }),
    });
    if (ask && !(await askConfirm(ask))) return;
    busy.value = true;
    const r = await runBulk(rows, act);
    busy.value = false;
    for (const row of r.ok) table.tick(row, false);
    note.value = t("common.bulk.done", { ok: r.ok.length, failed: r.failed.length }) + (r.failed.length > 0 ? ` · ${t("common.bulk.notChanged")}: ${r.failed.map((f) => opts.name(f.row)).join(", ")}` : "");
  }
  async function selectAll() {
    if (loading.value) return;
    loading.value = true;
    try {
      await selectAllResults(() => table.selectAll(), opts.rest);
    } catch {
      note.value = t("common.bulk.loadFailed");
    } finally {
      loading.value = false;
    }
  }
  const bar = computed(() => ({
    count: table.selectedRows.value.length,
    total: table.total.value,
    offer: table.canSelectAll.value || (!!opts.rest?.has() && table.allOnPage.value),
    more: !!opts.rest?.has(),
    loading: loading.value,
  }));
  return { note, busy, run, selectAll, bar };
}
