import { fold } from "@/shared/lib/intl";

/** What SearchSelect needs of an option (see SearchSelect.vue). */
export interface FilterOption {
  value: string;
  label: string;
  short?: string;
  hint?: string;
  keywords?: string;
}

/** Makes an option out of what was typed, or null when the text is not acceptable (SearchSelect's `custom`). */
export type CustomOption = (query: string) => FilterOption | null;

const text = (o: FilterOption) => fold(`${o.label} ${o.hint ?? ""} ${o.value} ${o.keywords ?? ""}`);

/**
 * The options a query shows: every word must match the label, the hint, the value or the keywords (case and accents ignored). An option whose value or
 * label is exactly the query comes first ("de" finds Germany before Bangladesh). With `custom`, what was typed is offered after the matches, unless an
 * option already is it; an empty choice that matches comes last.
 */
export function filterOptions<T extends FilterOption>(options: readonly T[], query: string, custom?: CustomOption): (T | FilterOption)[] {
  const words = fold(query).split(/\s+/).filter(Boolean);
  if (words.length === 0) return [...options];
  const want = fold(query.trim());
  const exact: T[] = [];
  const rest: T[] = [];
  const blank: T[] = [];
  for (const o of options) {
    const t = text(o);
    if (!words.every((w) => t.includes(w))) continue;
    if (o.value === "") blank.push(o);
    else (fold(o.value) === want || fold(o.label) === want ? exact : rest).push(o);
  }
  const out: (T | FilterOption)[] = [...exact, ...rest];
  const typed = custom?.(query);
  if (typed && !options.some((o) => fold(o.value) === fold(typed.value))) out.push(typed);
  // The empty choice ("No country", "Any currency") matches its own words, but is never what a search is for: it comes last.
  return [...out, ...blank];
}

/**
 * What leaving the field after typing chooses: the option whose value or label is the text, else the typed option, else nothing (undefined keeps the old
 * value). Empty text clears the field ("").
 */
export function resolveTyped(options: readonly FilterOption[], query: string, custom?: CustomOption): string | undefined {
  const want = fold(query.trim());
  if (want === "") return "";
  const hit = options.find((o) => fold(o.value) === want || fold(o.label) === want || (o.short !== undefined && fold(o.short) === want));
  if (hit) return hit.value;
  return custom?.(query)?.value;
}
