import type { Composer } from "vue-i18n";

/** "ENVIRONMENT_SANDBOX" -> "sandbox", "TENANT_TIER_FREE" -> "free": the lower-case word the locale files key their labels by. */
export function enumWord(value: string | undefined, prefix: string): string {
  return (value ?? "").replace(prefix, "").toLowerCase();
}

/** A translated label for a backend enum value, or the value itself when the locale has none (new backend values still show). */
export function label(t: Composer["t"], te: Composer["te"], group: string, key: string | undefined): string {
  if (!key) return "";
  const path = `enums.${group}.${key}`;
  return te(path) ? t(path) : key;
}
