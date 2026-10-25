import { parseMajor } from "@/shared/lib/money";

/**
 * The minor units of the amount typed into the credit note form: "0" for an empty field (the whole of what is left of the invoice), null when it is not a
 * positive amount in the invoice's currency. With no known currency nothing is read: a guessed one could put the decimal point in the wrong place.
 */
export function creditAmountMinor(text: string, currency: string | undefined): string | null {
  if (text.trim() === "") return "0";
  if (!currency) return null;
  const parsed = parseMajor(text, currency);
  return parsed === null || parsed === "0" ? null : parsed;
}
