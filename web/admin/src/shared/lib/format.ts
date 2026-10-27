import { useI18n } from "vue-i18n";
import { formatMinor } from "./money";

/**
 * The one place the console turns dates, counts and percentages into text. A page asks for a style by name (not for Intl options), and the formatters are
 * made once per locale and style, not on every call. A missing or unreadable date gives the page's `empty` text and never throws (Intl refuses an
 * invalid date with a RangeError, which would take the whole page down).
 */
export type DateStyle =
  /** 21 Oct 2026 */
  | "day"
  /** 21 Oct 2026, 09:05 */
  | "dateTime"
  /** 21 Oct 2026, 09:05 (the form lists use; the same date as "day") */
  | "short"
  /** 21 Oct 2026, 09:05:30 */
  | "shortSeconds"
  /** 21 Oct (no year: a list of upcoming days) */
  | "monthDay";

const OPTIONS: Record<DateStyle, Intl.DateTimeFormatOptions> = {
  day: { dateStyle: "medium" },
  dateTime: { dateStyle: "medium", timeStyle: "short" },
  // The compact styles write the day like every other date ("21 Oct 2026"); only the time part differs, so lists and tables never mix 21/10/2026 and 21 Oct 2026.
  short: { dateStyle: "medium", timeStyle: "short" },
  shortSeconds: { dateStyle: "medium", timeStyle: "medium" },
  monthDay: { day: "numeric", month: "short" },
};

const dateFormats = new Map<string, Intl.DateTimeFormat>();
const numberFormats = new Map<string, Intl.NumberFormat>();

function dateFormat(locale: string, style: DateStyle): Intl.DateTimeFormat {
  const key = `${locale}|${style}`;
  let f = dateFormats.get(key);
  if (!f) {
    f = new Intl.DateTimeFormat(locale, OPTIONS[style]);
    dateFormats.set(key, f);
  }
  return f;
}

function numberFormat(locale: string, oneDecimal: boolean): Intl.NumberFormat {
  const key = `${locale}|${oneDecimal ? "1" : "0"}`;
  let f = numberFormats.get(key);
  if (!f) {
    f = new Intl.NumberFormat(locale, oneDecimal ? { maximumFractionDigits: 1 } : undefined);
    numberFormats.set(key, f);
  }
  return f;
}

/** An instant (an RFC 3339 text from the API) as text in the reader's time zone; `empty` for nothing or for something that is not a date. */
export function formatDate(value: string | null | undefined, locale: string, style: DateStyle = "day", empty = ""): string {
  if (!value) return empty;
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? empty : dateFormat(locale, style).format(at);
}

/** A calendar day sent as 2026-10-21 (no time zone: the server counts UTC days) as text, `day` or `monthDay` style, without shifting it by the reader's zone. */
export function formatUtcDay(value: string | null | undefined, locale: string, style: "day" | "monthDay" | "month" = "day", empty = ""): string {
  if (!value) return empty;
  const at = new Date(`${value}T00:00:00Z`);
  if (Number.isNaN(at.getTime())) return empty;
  const options: Intl.DateTimeFormatOptions = style === "month" ? { month: "short", year: "numeric", timeZone: "UTC" } : { ...OPTIONS[style], timeZone: "UTC" };
  return new Intl.DateTimeFormat(locale, options).format(at);
}

/** A whole number with the locale's separators; `empty` for a missing one. */
export function formatCount(n: number | null | undefined, locale: string): string {
  return numberFormat(locale, false).format(n ?? 0);
}

/** A percentage the API computed ("12.3", at most one decimal) with the sign of the locale; "—" when there is none. */
export function formatPercent(v: number | null | undefined, locale: string): string {
  return v === null || v === undefined ? "—" : `${numberFormat(locale, true).format(v)}%`;
}

/**
 * The formatters of the page's own language: `const fmt = useFormat()` then `fmt.date(v)`, `fmt.date(v, "dateTime", "—")`, `fmt.money(minor, currency)`,
 * `fmt.amount(minor, currency)`, `fmt.count(n)`, `fmt.percent(p)`. Amounts go through `formatMinor`, so privacy mode applies to them.
 */
export function useFormat() {
  const { locale } = useI18n();
  return {
    date: (value: string | null | undefined, style: DateStyle = "day", empty = "") => formatDate(value, locale.value, style, empty),
    utcDay: (value: string | null | undefined, style: "day" | "monthDay" | "month" = "day", empty = "") => formatUtcDay(value, locale.value, style, empty),
    count: (n: number | null | undefined) => formatCount(n, locale.value),
    percent: (v: number | null | undefined) => formatPercent(v, locale.value),
    money: (minor: string | number | undefined, currency: string | undefined) => formatMinor(minor, currency, locale.value),
    /** Like `money`, but a missing figure counts as zero (a total that has no rows yet). */
    amount: (minor: string | number | null | undefined, currency: string | undefined) => formatMinor(minor ?? 0, currency, locale.value),
  };
}
