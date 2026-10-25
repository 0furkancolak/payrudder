/** The grace period as the API takes it: a protobuf Duration in JSON, whole seconds with an "s" ("259200s" for 72 hours). */
export function graceDuration(hours: number): string {
  return `${hours * 3600}s`;
}

/** Hours in a protobuf Duration ("259200s" is 72), or null for something that is not whole seconds. A part of an hour is kept ("5400s" is 1.5). */
export function durationHours(duration: string | undefined): number | null {
  const m = /^(\d+)(?:\.\d+)?s$/.exec(duration ?? "");
  return m ? Number(m[1]) / 3600 : null;
}

/** A number of hours in the reader's language ("72 hours", "3 Stunden"); the raw text when it is not a duration. */
export function formatDuration(duration: string | undefined, locale: string): string {
  const hours = durationHours(duration);
  if (hours === null) return duration ?? "";
  try {
    return new Intl.NumberFormat(locale, { style: "unit", unit: "hour", unitDisplay: "long", maximumFractionDigits: 2 }).format(hours);
  } catch {
    return `${hours} h`;
  }
}
