/**
 * The matching of the command palette (⌘K): a query finds an entry when every word of it is in the entry's text, ignoring case and accents, so
 * "odeme" finds "Ödemeler" and "musteri" finds "Müşteriler". The dotless and dotted Turkish i both fold to "i".
 */
export function fold(text: string): string {
  return text
    .toLowerCase()
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/ı/g, "i");
}

export function matches(query: string, text: string): boolean {
  const hay = fold(text);
  return fold(query)
    .split(/\s+/)
    .filter(Boolean)
    .every((word) => hay.includes(word));
}
