import type { components } from "@/shared/api/schema";

type EventErrorCode = NonNullable<components["schemas"]["EventResult"]["error_code"]>;

/**
 * Every `error_code` a rejected usage event can carry, each with a hint of what to do under `connect.eventErrors.<code>` in the six locales (the developer docs show the English
 * hint from the same place). The type check below fails the build when the API gains a code that is not listed here, so none is shown without advice.
 */
export const EVENT_ERROR_CODES = [
  "UNKNOWN_METER",
  "UNKNOWN_CUSTOMER",
  "EVENT_TOO_OLD",
  "EVENT_IN_FUTURE",
  "INVALID_EVENT_TIME",
  "INVALID_QUANTITY",
  "INVALID_QUANTITY_PRECISION",
  "IDEMPOTENCY_KEY_REQUIRED",
  "IDEMPOTENCY_KEY_INVALID",
  "IDEMPOTENCY_KEY_REUSED",
  "INVALID_PROPERTIES",
  "INVALID_CORRECTION",
  "CORRECTION_UNKNOWN_EVENT",
  "CORRECTION_MISMATCH",
  "CORRECTION_UNSUPPORTED_METER",
  "CORRECTION_OF_CORRECTION",
] as const satisfies readonly EventErrorCode[];

const _everyCodeIsListed: Exclude<EventErrorCode, (typeof EVENT_ERROR_CODES)[number]> extends never ? true : never = true;
void _everyCodeIsListed;

/** The locale key of the hint for a code, or null for a code this console does not know (the API's own message is still shown). */
export function eventErrorHintKey(code: string | undefined): string | null {
  return (EVENT_ERROR_CODES as readonly string[]).includes(code ?? "") ? `connect.eventErrors.${code}` : null;
}
