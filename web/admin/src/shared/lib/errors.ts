import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";

/** The two parts of a vue-i18n composer this needs, so a test can pass a bare one. */
type Translate = (key: string, named: Record<string, unknown>) => string;
type Exists = (key: string) => boolean;

/**
 * The text for an error from the API. The gateway sends a stable key (`billing.credit_note.not_paid`) next to an English message; the key is translated
 * through `errors.<key>` in the locale files (docs/error-keys.md lists them) with the error's params for placeholders, and when the locale has no
 * entry (a key newer than the console, or an error without one) the server's message is shown instead, so nothing is ever hidden.
 */
export function errorText(t: Translate, te: Exists, error: unknown): string {
  const e = toApiError(error);
  const path = `errors.${e.key}`;
  if (e.key && te(path)) return t(path, { ...e.params });
  return e.message;
}

/** `errorText` bound to the current locale: the one function every page and toast uses to show an API error. */
export function useErrorText(): (error: unknown) => string {
  const { t, te } = useI18n();
  return (error) => errorText(t as Translate, te, error);
}
