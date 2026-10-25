/** Rules the server enforces that the console checks before it sends, so a refusal that can be foreseen never travels. One home, one number. */

/** How long (days) a payment, invoice or portal link may be valid. */
export const LINK_DAYS = { min: 1, max: 365 } as const;

/** Whether a typed number of days is a whole number the server accepts for a link. */
export function validLinkDays(days: number): boolean {
  return Number.isInteger(days) && days >= LINK_DAYS.min && days <= LINK_DAYS.max;
}

/** A positive whole number of credits (up to 12 digits: the server holds them as int64 text). */
export const CREDITS_RE = /^[1-9]\d{0,11}$/;
/** The same, or zero (a threshold that is off). */
export const CREDITS_OR_ZERO_RE = /^(0|[1-9]\d{0,11})$/;

/** The longest a pack of credits may last once bought (days), and the most packs one purchase link may carry. */
export const CREDIT_VALIDITY_MAX_DAYS = 3650;
export const PURCHASE_QUANTITY_MAX = 1000;
