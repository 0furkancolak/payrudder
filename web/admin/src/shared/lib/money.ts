/**
 * Formats an amount in minor units (a decimal string, as the API sends int64) for a currency and a locale. The digits are moved
 * as text, never through a float: the string goes to Intl.NumberFormat, which formats decimal strings exactly.
 */
export function formatMinor(minor: string | number | undefined, currency: string | undefined, locale: string): string {
  if (minor === undefined || minor === "") return "";
  const raw = String(minor);
  if (!currency || !/^-?\d+$/.test(raw)) return raw;
  let formatter: Intl.NumberFormat;
  try {
    formatter = new Intl.NumberFormat(locale, { style: "currency", currency });
  } catch {
    return `${currency} ${raw}`;
  }
  const digits = formatter.resolvedOptions().maximumFractionDigits ?? 2;
  const negative = raw.startsWith("-");
  const abs = negative ? raw.slice(1) : raw;
  const padded = abs.padStart(digits + 1, "0");
  const whole = padded.slice(0, padded.length - digits);
  const frac = digits > 0 ? padded.slice(padded.length - digits) : "";
  const decimal = `${negative ? "-" : ""}${whole}${frac ? "." + frac : ""}`;
  return formatter.format(decimal as unknown as number);
}

/** How many digits an amount in a currency has after the point (2 for dollars, 0 for yen, 3 for dinar). */
export function minorDigits(currency: string, locale = "en"): number {
  try {
    return new Intl.NumberFormat(locale, { style: "currency", currency }).resolvedOptions().maximumFractionDigits ?? 2;
  } catch {
    return 2;
  }
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
