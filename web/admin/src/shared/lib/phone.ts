/**
 * A mobile number in international form, as `pkg/phone` in Go reads it (the same cases, phone.test.ts): spaces, dots, dashes and brackets are dropped, a leading `+` or
 * `00` is the international prefix, 7 to 15 digits follow and the first is not 0. A national spelling ("0532 123 45 67") is refused: without a country it cannot be read.
 * Empty is fine (the number is optional).
 */
export function normalizePhone(raw: string): string | null {
  const s = raw.replace(/[  .\-()\t]/g, "");
  if (s === "") return "";
  const digits = s.startsWith("+") ? s.slice(1) : s.startsWith("00") ? s.slice(2) : null;
  return digits !== null && /^[1-9]\d{6,14}$/.test(digits) ? `+${digits}` : null;
}
