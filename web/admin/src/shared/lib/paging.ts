/** The page numbers a pager shows: the first, the last, the current one with its neighbours, and an ellipsis where numbers are left out. */
export type PageItem = number | "ellipsis";

const range = (from: number, to: number): number[] => Array.from({ length: Math.max(0, to - from + 1) }, (_, i) => from + i);

export function pageRange(current: number, total: number, siblings = 1): PageItem[] {
  if (siblings * 2 + 5 >= total) return range(1, total);
  const left = Math.max(current - siblings, 1);
  const right = Math.min(current + siblings, total);
  const leftGap = left > 2;
  const rightGap = right < total - 1;
  if (!leftGap && rightGap) return [...range(1, siblings * 2 + 3), "ellipsis", total];
  if (leftGap && !rightGap) return [1, "ellipsis", ...range(total - (siblings * 2 + 3) + 1, total)];
  if (leftGap && rightGap) return [1, "ellipsis", ...range(left, right), "ellipsis", total];
  return range(1, total);
}

export const PAGE_SIZES = [10, 25, 50, 100] as const;

/** The slice of rows on a page (pages count from 1) and the number of pages; a page past the end falls back to the last one. */
export function paginate<T>(rows: readonly T[], page: number, size: number): { items: T[]; pages: number; page: number } {
  const pages = Math.max(1, Math.ceil(rows.length / size));
  const at = Math.min(Math.max(1, page), pages);
  return { items: rows.slice((at - 1) * size, at * size), pages, page: at };
}

/** Whether a row matches a search text: every word must be found, in any of the given fields, ignoring case. */
export function matchesSearch(fields: readonly (string | number | null | undefined)[], text: string): boolean {
  const words = text.toLocaleLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const hay = fields.map((f) => String(f ?? "").toLocaleLowerCase()).join("\u0000");
  return words.every((w) => hay.includes(w));
}
