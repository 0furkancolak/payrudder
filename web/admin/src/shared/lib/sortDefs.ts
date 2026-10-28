import type { SortDef } from "./useTableState";

/** The usual choices of a sort select, worded by `common.sort.*`. `t` is the page's translate function; `by` reads the column from a row. */
type T = (key: string) => string;
type By<R> = SortDef<R>["by"];

/** An instant (RFC 3339 text) as a number to compare; null for nothing or something that is not a date. */
export const time = (v: string | null | undefined): number | null => {
  if (!v) return null;
  const at = Date.parse(v);
  return Number.isNaN(at) ? null : at;
};
/** An amount sent as text of minor units as a number to compare. */
export const amount = (v: string | number | null | undefined): number | null => (v === null || v === undefined || v === "" ? null : Number(v));

export const newest = <R>(t: T, by: By<R>): SortDef<R> => ({ key: "newest", label: t("common.sort.newest"), by, dir: "desc" });
export const oldest = <R>(t: T, by: By<R>): SortDef<R> => ({ key: "oldest", label: t("common.sort.oldest"), by, dir: "asc" });
export const amountHigh = <R>(t: T, by: By<R>): SortDef<R> => ({ key: "amount_desc", label: t("common.sort.amountHigh"), by, dir: "desc" });
export const amountLow = <R>(t: T, by: By<R>): SortDef<R> => ({ key: "amount_asc", label: t("common.sort.amountLow"), by, dir: "asc" });
export const nameAsc = <R>(t: T, by: By<R>): SortDef<R> => ({ key: "name_asc", label: t("common.sort.nameAsc"), by, dir: "asc" });
export const nameDesc = <R>(t: T, by: By<R>): SortDef<R> => ({ key: "name_desc", label: t("common.sort.nameDesc"), by, dir: "desc" });
