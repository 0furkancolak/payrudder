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
