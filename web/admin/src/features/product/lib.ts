import type { AppOverview, Environment } from "./api";

/** The colours of the payment statuses in the donut: the pastel brand blue for money that arrived, warm and red tints for the rest. */
export const STATUS_COLORS: Record<string, string> = {
  paid: "var(--brand)",
  processing: "oklch(0.82 0.12 85)",
  failed: "oklch(0.7 0.16 25)",
  refunded: "oklch(0.72 0.05 260)",
};

export const PAYMENT_STATUSES = ["paid", "processing", "failed", "refunded"] as const;

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

export function totalPayments(payments: Payments | null | undefined): number {
  return PAYMENT_STATUSES.reduce((n, s) => n + countOf(payments, s), 0);
}

/** Environments in the order the switcher shows them. */
export const ENVIRONMENTS: Environment[] = ["live", "sandbox"];

export function isEnvironment(v: unknown): v is Environment {
  return v === "live" || v === "sandbox";
}

/** The tenant of an app for an environment. */
export function tenantOf(app: { tenants?: { id?: string; environment?: string }[] } | undefined, env: Environment): string {
  const want = env === "live" ? "ENVIRONMENT_LIVE" : "ENVIRONMENT_SANDBOX";
  return app?.tenants?.find((t) => t.environment === want)?.id ?? "";
}

/** Timezones offered in the create-app dialog: the browser's own first, then a short list of common ones. */
export function timezoneChoices(): string[] {
  let own = "UTC";
  try {
    own = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    // keep UTC
  }
  const common = ["UTC", "Europe/Istanbul", "Europe/London", "Europe/Berlin", "Europe/Paris", "Europe/Madrid", "America/New_York", "America/Chicago", "America/Los_Angeles", "Asia/Dubai", "Asia/Tokyo", "Asia/Singapore", "Australia/Sydney"];
  return [own, ...common.filter((z) => z !== own)];
}
