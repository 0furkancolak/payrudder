import { CURRENCY_EXPONENTS, type Currency } from "./currencies";
import { privacyMode } from "./prefs";

/** What privacy mode shows in place of the figures of an amount. A fixed width, so the mask does not tell the size of the amount. */
export const MASK = "••••";

/** Replaces every run of figures (with its separators) in an already formatted text by the mask; the sign and the currency stay. */
export function maskDigits(text: string): string {
  return text.replace(/\p{Nd}(?:[\p{Nd}.,٫٬'’   ]*\p{Nd})?/gu, MASK);
}

/** An e-mail address as privacy mode shows it: the first letter of the name and the domain ("a•••@example.com"). */
export function maskEmail(email: string | undefined | null): string {
  if (!email) return "";
  if (!privacyMode.value) return email;
  const at = email.lastIndexOf("@");
  return at < 1 ? MASK : `${email.slice(0, 1)}•••${email.slice(at)}`;
}

/**
 * Formats an amount in minor units (a decimal string, as the API sends int64) for a currency and a locale. The figures are moved
 * as text, never through a float: the string goes to Intl.NumberFormat, which formats decimal strings exactly.
 *
 * Every amount the console shows goes through here, so privacy mode is one switch: while it is on, the figures are replaced by a mask in the text itself
 * (not hidden by styling), which keeps them out of the page, of a screenshot, of the print preview and of what a screen reader says. The function reads
 * the reactive flag, so a template that calls it draws again when the flag changes.
 */
export function formatMinor(minor: string | number | undefined, currency: string | undefined, locale: string): string {
  const text = formatAmount(minor, currency, locale);
  return privacyMode.value ? maskDigits(text) : text;
}

function formatAmount(minor: string | number | undefined, currency: string | undefined, locale: string): string {
  if (minor === undefined || minor === "") return "";
  const raw = String(minor);
  if (!currency || !/^-?\d+$/.test(raw)) return raw;
  let formatter: Intl.NumberFormat;
  let digits: number;
  try {
    digits = minorDigits(currency);
    formatter = new Intl.NumberFormat(locale, { style: "currency", currency, minimumFractionDigits: digits, maximumFractionDigits: digits });
  } catch {
    return `${currency} ${raw}`;
  }
  const negative = raw.startsWith("-");
  const abs = negative ? raw.slice(1) : raw;
  const padded = abs.padStart(digits + 1, "0");
  const whole = padded.slice(0, padded.length - digits);
  const frac = digits > 0 ? padded.slice(padded.length - digits) : "";
  const decimal = `${negative ? "-" : ""}${whole}${frac ? "." + frac : ""}`;
  const out = formatter.format(decimal as unknown as number);
  // In a right-to-left page the sign and the currency of an amount land on the wrong side of the digits ("TRY 30.00-"); an amount is read left to right, so isolate it.
  return /^(ar|he|fa|ur)\b/.test(locale) ? `⁦${out}⁩` : out;
}

/**
 * How many digits an amount in a currency has after the point (2 for dollars, 0 for yen, 3 for dinar). From the backend's table (CURRENCY_EXPONENTS,
 * pkg/money), never from Intl: ICU's fraction digits differ for HUF, IQD and others, and a wrong one is a 100x or 1000x error.
 * A code the table does not know (the backend would refuse it) counts as 2.
 */
export function minorDigits(currency: string): number {
  return CURRENCY_EXPONENTS[currency as Currency] ?? 2;
}

/**
 * Reads an amount a person typed ("12.5", "12,50", "1 250.00") in a currency into minor units as a decimal string, or null when it is not a non-negative
 * amount with no more digits after the point than the currency has. Done on the text: no float.
 */
export function parseMajor(input: string, currency: string): string | null {
  const digits = minorDigits(currency);
  const text = input.trim().replace(/[\s\u00a0']/g, "");
  const m = /^(\d+)(?:[.,](\d+))?$/.exec(text);
  if (!m) return null;
  const whole = m[1] ?? "";
  const frac = m[2] ?? "";
  if (frac.length > digits) return null;
  const minor = (whole + frac.padEnd(digits, "0")).replace(/^0+(?=\d)/, "");
  return minor === "" ? "0" : minor;
}

/**
 * An amount in minor units (a decimal string) as the plain decimal a person types for a currency ("1250" -> "12.50" for dollars, "500" -> "500" for yen).
 * The inverse of parseMajor; done on the text, no float. Anything that is not a non-negative integer gives "".
 */
export function minorToMajor(minor: string | number | undefined, currency: string): string {
  const raw = String(minor ?? "");
  if (!/^\d+$/.test(raw)) return "";
  const digits = minorDigits(currency);
  const padded = raw.padStart(digits + 1, "0");
  const whole = padded.slice(0, padded.length - digits).replace(/^0+(?=\d)/, "");
  return digits > 0 ? `${whole}.${padded.slice(padded.length - digits)}` : whole;
}

/**
 * A short form of an amount for a chart's axis ("$1.2K", "¥3.4M"): the amount in major units through Intl's compact notation. For labels only; the exact
 * amounts are always shown with formatMinor. Masked like it in privacy mode.
 */
export function formatCompact(minor: number, currency: string | undefined, locale: string): string {
  let out: string;
  try {
    const digits = minorDigits(currency ?? "USD");
    out = new Intl.NumberFormat(locale, { style: "currency", currency: currency ?? "USD", notation: "compact", maximumFractionDigits: 1 }).format(minor / 10 ** digits);
  } catch {
    out = String(minor);
  }
  return privacyMode.value ? maskDigits(out) : out;
}
