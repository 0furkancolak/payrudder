import type { AppOverview } from "./api";

type Payments = NonNullable<AppOverview["payments"]>;

/** Money that arrived in the window, one entry per currency, the environment's own currency first. Never summed across currencies. */
export function revenueByCurrency(o: Pick<AppOverview, "currency"> & { payments?: Payments | null }): { currency: string; minor: number }[] {
  const rows = (o.payments?.by_currency ?? []).map((c) => ({ currency: c.currency ?? "", minor: c.paid_minor ?? 0 }));
  return rows.sort((a, b) => Number(b.currency === o.currency) - Number(a.currency === o.currency) || b.minor - a.minor || a.currency.localeCompare(b.currency));
}

/** The daily series of a currency for the sparkline (zeros when the currency has none). */
export function seriesOf(payments: Payments | null | undefined, currency: string): number[] {
  const s = payments?.series?.find((x) => x.currency === currency);
  return (s?.points ?? []).map((p) => p.amount_minor ?? 0);
}

/** The count of a payment status in the donut. */
export function countOf(payments: Payments | null | undefined, status: string): number {
  return payments?.by_status?.find((s) => s.status === status)?.count ?? 0;
}

/** The reasons /signin?oauth_error= can carry (the gateway's Google callback sends one of them); anything else in the address is ignored. */
export const OAUTH_ERRORS = ["state", "denied", "token", "email_unverified", "unavailable", "failed"];

// --- the shell: memberships of the role "affiliate" are not accounts of the person

interface MembershipLike {
  account_id?: string;
  role?: string;
}

/** The roles a seller's account gives a partner: they see none of the account, so the account is not the person's to list, switch to or load apps of. */
export const AFFILIATE_ROLE = "affiliate";

export function ownMemberships<T extends MembershipLike>(ms: readonly T[] | undefined): T[] {
  return (ms ?? []).filter((m) => m.role !== AFFILIATE_ROLE);
}

export const hasPartnerRole = (ms: readonly MembershipLike[] | undefined): boolean => (ms ?? []).some((m) => m.role === AFFILIATE_ROLE);

// --- the return path of the sign-in

/** A path the sign-in may send the person back to: this console only (one leading slash), never the sign-in pages themselves, and nothing odd. */
export function safeNext(value: unknown): string {
  if (typeof value !== "string" || value.length > 500) return "";
  if (!value.startsWith("/") || value.startsWith("//") || value.includes("\\") || /[\u0000-\u001f]/.test(value)) return "";
  if (/^\/(signin|signup|forgot|reset)(\/|\?|$)/.test(value)) return "";
  return value;
}

/** The page to come back to after signing in: what safeNext allows, except the home itself, which is where signing in goes anyway (a clean address). */
export function returnTo(href: unknown): string {
  const next = safeNext(href);
  return next === "/app" ? "" : next;
}
