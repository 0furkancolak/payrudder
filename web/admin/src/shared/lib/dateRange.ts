export type RangePreset = "today" | "7d" | "30d" | "month" | "lastMonth" | "year";

const ymd = (d: Date): string => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
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
export function inRange(when: string | undefined, r: { from: string; to: string }): boolean {
  if (r.from === "" && r.to === "") return true;
  if (!when) return false;
  const d = new Date(when);
  if (Number.isNaN(d.getTime())) return false;
  const day = ymd(d);
  return (r.from === "" || day >= r.from) && (r.to === "" || day <= r.to);
}
