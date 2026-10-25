/** Credits are int64 on the server and travel as decimal text; they are formatted from the text, never through a float. */
export function formatCredits(value: string | number | undefined, locale: string): string {
  if (value === undefined || value === "") return "";
  try {
    return new Intl.NumberFormat(locale).format(BigInt(value));
  } catch {
    return String(value);
  }
}

/** The badge of a ledger entry's kind: a grant is good news, an expiry is muted, everything else (spending, adjustments) is plain. */
const KIND_VARIANTS = { grant: "success", expire: "secondary" } as const;
export function kindVariant(kind: string | undefined): "success" | "secondary" | "outline" {
  return KIND_VARIANTS[kind as keyof typeof KIND_VARIANTS] ?? "outline";
}

/** A pack's price and a purchase's quantity are checked before sending; the validity is optional (empty: the credits never expire). */
export function validityDays(text: string, max: number): number | null | undefined {
  const t = text.trim();
  if (t === "") return null;
  if (!/^\d{1,4}$/.test(t)) return undefined;
  const n = Number(t);
  return n >= 1 && n <= max ? n : undefined;
}

/** Whether a quantity typed for a purchase link is a whole number from 1 to `max`. */
export function validQuantity(text: string, max: number): boolean {
  const n = Number(text);
  return Number.isInteger(n) && n >= 1 && n <= max;
}
