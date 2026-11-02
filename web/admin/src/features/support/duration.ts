/**
 * A length of time in the one unit that reads best, written the way the language writes it ("45 min", "3 h", "2 d", Turkish "45 dk"): seconds under a minute, minutes
 * under an hour, hours under a day, days above. Rounded; the report's figures are for reading, not for adding up.
 */
export function duration(seconds: number, locale: string): string {
  const units: [number, string][] = [
    [86_400, "day"],
    [3_600, "hour"],
    [60, "minute"],
    [1, "second"],
  ];
  const [size, unit] = units.find(([n]) => seconds >= n) ?? units[units.length - 1]!;
  return new Intl.NumberFormat(locale, { style: "unit", unit, unitDisplay: "narrow", maximumFractionDigits: 0 }).format(Math.round(seconds / size));
}
