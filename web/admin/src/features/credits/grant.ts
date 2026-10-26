/**
 * Free credits: what a grant is for and when it runs out, as the grant sheet offers them. A grant is a gift (not invoiced); the ledger shows its reason
 * to whoever reads it later, so the reason is a stable English phrase plus the note, whatever language the console was in when it was made.
 */
export const REASONS = ["welcome", "goodwill", "promotion", "other"] as const;
export type Reason = (typeof REASONS)[number];

const PHRASE: Record<Reason, string> = { welcome: "Welcome gift", goodwill: "Goodwill", promotion: "Promotion", other: "" };
/** Which kind of lot a reason makes: a welcome gift and a promotion are promotional credits, the rest are manual adjustments. */
const SOURCE: Record<Reason, "promo" | "manual"> = { welcome: "promo", goodwill: "manual", promotion: "promo", other: "manual" };

export const sourceOf = (reason: Reason) => SOURCE[reason];

/** The reason the ledger keeps: the phrase, then the person's note ("Welcome gift: first month"), at most 200 characters (the API's limit). */
export function reasonText(reason: Reason, note: string): string | undefined {
  const text = [PHRASE[reason], note.trim()].filter(Boolean).join(": ");
  return text === "" ? undefined : text.slice(0, 200);
}

export const EXPIRY_CHOICES = ["never", "30", "90", "365", "date"] as const;
export type ExpiryChoice = (typeof EXPIRY_CHOICES)[number];

/**
 * When the credits run out, as the timestamp the API takes: the end of the day, in the person's time zone, that many days from `now` or on the date they
 * picked. Empty (never) leaves it out. A date that is not in the future is `undefined`, which the form turns into a message.
 */
export function expiryTime(choice: ExpiryChoice, date: string, now: Date = new Date()): string | null | undefined {
  if (choice === "never") return null;
  let end: Date;
  if (choice === "date") {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return undefined;
    end = new Date(`${date}T23:59:59`);
  } else {
    end = new Date(now);
    end.setDate(end.getDate() + Number(choice));
    end.setHours(23, 59, 59, 0);
  }
  return Number.isNaN(end.getTime()) || end.getTime() <= now.getTime() ? undefined : end.toISOString();
}
