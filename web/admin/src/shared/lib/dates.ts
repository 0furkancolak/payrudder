/** Calendar days as the API takes them (2026-10-21) and the browser's own calendar they are read in. One home for the padding and the "today" tricks. */

const pad = (n: number) => String(n).padStart(2, "0");

/** A date as 2026-10-21 in the reader's calendar. */
export function ymd(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** Today as 2026-10-21 in the reader's calendar. */
export function todayYmd(now: Date = new Date()): string {
  return ymd(now);
}

/** The month of a date as 2026-10, the value of an <input type="month">. */
export function ym(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}`;
}

/** A date as the value of an <input type="datetime-local"> in the browser's time zone ("2026-10-20T14:05"). */
export function toLocalInput(d: Date): string {
  return `${ymd(d)}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
