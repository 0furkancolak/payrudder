/**
 * What a card needs to know about the reads it states things from ("connected", "no accounts", "no entries"): a read that is still going or has failed
 * must never be drawn as the empty or the not-connected answer. `readOf` takes a query, `together` folds several into one.
 */
export interface Read {
  error: unknown;
  /** Going for the first time: there is nothing to show yet. */
  loading: boolean;
  fetching: boolean;
  retry: () => unknown;
}

interface Query {
  error: { value: unknown };
  isLoading: { value: boolean };
  isFetching: { value: boolean };
  refetch: () => unknown;
}

export const readOf = (q: Query): Read => ({ error: q.error.value, loading: q.isLoading.value, fetching: q.isFetching.value, retry: () => q.refetch() });

/** The same for one result of `useQueries`, which is a plain object and not a set of refs. */
export const resultOf = (q: { error: unknown; isLoading: boolean; isFetching: boolean; refetch: () => unknown }): Read => ({ error: q.error, loading: q.isLoading, fetching: q.isFetching, retry: () => q.refetch() });

/** The first error, loading while any is, and a retry that asks again only the reads that failed. */
export function together(reads: readonly Read[]): Read {
  const failed = reads.filter((r) => r.error);
  return {
    error: failed[0]?.error ?? null,
    loading: reads.some((r) => r.loading),
    fetching: reads.some((r) => r.fetching),
    retry: () => Promise.all(failed.map((r) => r.retry())),
  };
}
