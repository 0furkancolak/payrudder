import { ref } from "vue";
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

/**
 * The bulk action of a list, the same everywhere: ask once when the action cannot be undone, run it on the ticked rows one by one, say "n done, m not changed"
 * with the names of the rows that failed, untick the ones that were done and leave the failed ones ticked so the same action can be repeated.
 * `selected` are the ticked rows still shown (useTableState `selectedRows`), `name` is what a row is called in the note.
 */
export function useBulk<T>(opts: { selected: () => readonly T[]; untick: (row: T) => void; name: (row: T) => string }) {
  const { t } = useI18n();
  const note = ref("");
  const busy = ref(false);
  /** `rows` narrows the ticked ones the action applies to (revoking skips the keys that are revoked already); `confirm` is the question, when there is one. */
  async function run(act: (row: T) => Promise<unknown>, o: { confirm?: ConfirmOptions; rows?: (all: readonly T[]) => readonly T[] } = {}) {
    const rows = o.rows ? o.rows(opts.selected()) : opts.selected();
    if (rows.length === 0 || busy.value) return;
    if (o.confirm && !(await askConfirm(o.confirm))) return;
    busy.value = true;
    const r = await runBulk(rows, act);
    busy.value = false;
    for (const row of r.ok) opts.untick(row);
    note.value = t("common.bulk.done", { ok: r.ok.length, failed: r.failed.length }) + (r.failed.length > 0 ? ` · ${t("common.bulk.notChanged")}: ${r.failed.map((f) => opts.name(f.row)).join(", ")}` : "");
  }
  return { note, busy, run };
}
