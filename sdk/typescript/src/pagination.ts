/**
 * Walks a token-paginated list and yields its items. `query.page_token` seeds the first page; iteration stops when the
 * server returns no `next_page_token` (or repeats the one it was given, which would never end).
 */
export async function* paginate<Q extends { page_token?: string }, P extends { next_page_token?: string }, T>(
  fetchPage: (query: Q) => Promise<P>,
  items: (page: P) => T[] | undefined,
  query: Q | undefined,
): AsyncGenerator<T, void, undefined> {
  let current = { ...query } as Q;
  for (;;) {
    const page = await fetchPage(current);
    for (const item of items(page) ?? []) yield item;
    const next = page.next_page_token;
    if (!next || next === current.page_token) return;
    current = { ...current, page_token: next };
  }
}
