/** The languages the API knows (the `locale` enum of its requests): the ones the console speaks. */
export const API_LOCALES = ["en", "tr", "de", "fr", "es", "ar"] as const;
export type ApiLocale = (typeof API_LOCALES)[number];

/** The page's language as the API takes it: the code itself when the API knows it, English otherwise (vue-i18n types its locale as any string). */
export function toApiLocale(locale: string): ApiLocale {
  const base = locale.toLowerCase().split("-")[0] ?? "";
  return (API_LOCALES as readonly string[]).includes(base) ? (base as ApiLocale) : "en";
}
