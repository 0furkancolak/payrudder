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
