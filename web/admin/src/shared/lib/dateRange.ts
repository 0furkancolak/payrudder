import { ymd } from "./dates";

export type RangePreset = "today" | "7d" | "30d" | "month" | "lastMonth" | "year";

const addDays = (d: Date, n: number): Date => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n);

/** The dates (YYYY-MM-DD, in the browser's own calendar) a preset stands for, counted from `now`; both ends are days that are inside the range. */
export function rangeOf(p: RangePreset, now: Date): { from: string; to: string } {
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  switch (p) {
    case "today":
      return { from: ymd(today), to: ymd(today) };
    case "7d":
      return { from: ymd(addDays(today, -6)), to: ymd(today) };
    case "30d":
      return { from: ymd(addDays(today, -29)), to: ymd(today) };
    case "month":
      return { from: ymd(new Date(today.getFullYear(), today.getMonth(), 1)), to: ymd(today) };
    case "lastMonth":
      return { from: ymd(new Date(today.getFullYear(), today.getMonth() - 1, 1)), to: ymd(new Date(today.getFullYear(), today.getMonth(), 0)) };
    case "year":
      return { from: ymd(new Date(today.getFullYear(), 0, 1)), to: ymd(today) };
  }
}

/** Whether a timestamp (RFC 3339, or a bare date) falls on a day inside the range; an empty end is open. */
export function inRange(when: string | null | undefined, r: { from: string; to: string }): boolean {
  if (r.from === "" && r.to === "") return true;
  if (!when) return false;
  const d = new Date(when);
  if (Number.isNaN(d.getTime())) return false;
  const day = ymd(d);
  return (r.from === "" || day >= r.from) && (r.to === "" || day <= r.to);
}

/** What a list's date filter starts as: the current month, from its first day to today. Clearing the filter shows every date. */
export const thisMonth = (now: Date = new Date()): { from: string; to: string } => rangeOf("month", now);

/** Whether a list's date filter is anything other than what it starts as; only then is there something to clear (and the clear goes back to the start, not to every date). */
export const rangeChanged = (r: { from: string; to: string }, now: Date = new Date()): boolean => {
  const d = thisMonth(now);
  return r.from !== d.from || r.to !== d.to;
};
