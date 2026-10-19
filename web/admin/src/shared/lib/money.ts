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
