import { createI18n } from "vue-i18n";
import { branded } from "@/shared/lib/brand";
import ar from "@/locales/ar.json";
import de from "@/locales/de.json";
import en from "@/locales/en.json";
import es from "@/locales/es.json";
import fr from "@/locales/fr.json";
import tr from "@/locales/tr.json";

export const messages = { en: branded(en), tr: branded(tr), de: branded(de), es: branded(es), fr: branded(fr), ar: branded(ar) } as const;
export type Locale = keyof typeof messages;
export const locales = Object.keys(messages) as Locale[];

const KEY = "quotify.locale";

export function isLocale(value: string | null | undefined): value is Locale {
  return value != null && (locales as string[]).includes(value);
}

/** The saved choice, else the first browser language we have, else English. */
export function detectLocale(): Locale {
  try {
    const saved = window.localStorage.getItem(KEY);
    if (isLocale(saved)) return saved;
  } catch {
    // storage blocked: fall through
  }
  for (const tag of navigator.languages ?? [navigator.language]) {
    const base = tag.toLowerCase().split("-")[0];
    if (isLocale(base)) return base;
  }
  return "en";
}

export const i18n = createI18n({
  legacy: false,
  locale: detectLocale(),
  fallbackLocale: "en",
  messages,
});

/** Applies a locale to the page: language, reading direction, document title, and remembers it. */
export function applyLocale(locale: Locale): void {
  i18n.global.locale.value = locale;
  document.documentElement.lang = locale;
  document.documentElement.dir = messages[locale].meta.dir;
  document.title = i18n.global.t("app.title");
  try {
    window.localStorage.setItem(KEY, locale);
  } catch {
    // storage blocked: the choice lasts for this page only
  }
}
