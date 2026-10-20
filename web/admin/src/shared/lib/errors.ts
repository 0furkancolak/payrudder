import type { Composer } from "vue-i18n";
import { toApiError } from "@/shared/api/client";

/**
 * The text for an error from the gateway: the translation of its stable key (errors.<key> in the locale files), else the server's message.
 * The console translates keys, never messages; a key nobody has translated yet still shows the message.
 */
export function errorText(t: Composer["t"], te: Composer["te"], error: unknown): string {
  const e = toApiError(error);
  const path = `errors.${e.key}`;
  if (e.key && te(path)) return t(path);
  return e.message;
}
