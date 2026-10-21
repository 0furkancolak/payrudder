import { createI18n } from "vue-i18n";
import { branded } from "@/shared/lib/brand";
import en from "@/locales/en.json";

/**
 * English is bundled (it is the fallback for any text a language lacks); the other five languages are fetched when somebody uses them, so a first load
 * carries one language, not six.
 */
const loaders = {
  tr: () => import("@/locales/tr.json"),
  de: () => import("@/locales/de.json"),
  es: () => import("@/locales/es.json"),
  fr: () => import("@/locales/fr.json"),
  ar: () => import("@/locales/ar.json"),
} as const;

/** Each language in its own name and its reading direction: known without loading the language. */
export const languages = {
  en: { name: "English", dir: "ltr" },
  tr: { name: "Türkçe", dir: "ltr" },
  de: { name: "Deutsch", dir: "ltr" },
  es: { name: "Español", dir: "ltr" },
  fr: { name: "Français", dir: "ltr" },
  ar: { name: "العربية", dir: "rtl" },
} as const;
export type Locale = keyof typeof languages;
export const locales = Object.keys(languages) as Locale[];

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
  locale: "en",
  fallbackLocale: "en",
  messages: { en: branded(en) },
});

const loaded = new Set<Locale>(["en"]);

async function load(locale: Locale): Promise<void> {
  if (loaded.has(locale) || locale === "en") return;
  const mod = await loaders[locale]();
  i18n.global.setLocaleMessage(locale, branded(mod.default) as never);
  loaded.add(locale);
}

/** Applies a locale to the page: loads it if needed, then language, reading direction, document title; and remembers it. */
export async function applyLocale(locale: Locale): Promise<void> {
  try {
    await load(locale);
  } catch {
    // The file could not be fetched (offline): stay in the language that is showing.
    return;
  }
  (i18n.global.locale as { value: string }).value = locale;
  document.documentElement.lang = locale;
  document.documentElement.dir = languages[locale].dir;
  document.title = i18n.global.t("app.title");
  try {
    window.localStorage.setItem(KEY, locale);
  } catch {
    // storage blocked: the choice lasts for this page only
  }
}
