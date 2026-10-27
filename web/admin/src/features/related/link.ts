import { tenantBase } from "@/shared/lib/session";

/** The lists a sheet can send the reader to; each opens the sheet of the record the `key` parameter names (see useOpenParam). */
const PAGES = {
  invoice: { page: "invoices", key: "invoice" },
  customer: { page: "customers", key: "customer" },
  subscription: { page: "subscriptions", key: "subscription" },
  payment: { page: "payments", key: "payment" },
  paymentLink: { page: "payment-links", key: "link" },
} as const;

/** What a link keeps of the address it leaves: the environment the person is looking at (the other page must not fall back to the last one used). */
export function keepEnv(prev: Record<string, unknown>, key: string, id: string): Record<string, unknown> {
  return { ...(prev.env === "sandbox" || prev.env === "live" ? { env: prev.env } : {}), [key]: id };
}

/**
 * Where a link to a record goes: the record's list page, with the record's id in the parameter that opens its sheet, in the environment of the page the
 * link is on. A person's pages are under /app/t, the operator's under /tenants.
 */
export function recordLink(kind: keyof typeof PAGES, id: string, tenantId = ""): { to: string; params: { id: string }; search: never } {
  const { page, key } = PAGES[kind];
  return { to: `${tenantBase()}/$id/${page}`, params: { id: tenantId }, search: ((prev: Record<string, unknown>) => keepEnv(prev, key, id)) as never };
}
