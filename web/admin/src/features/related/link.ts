import { tenantBase } from "@/shared/lib/session";

/** The lists a sheet can send the reader to; each opens the sheet of the record the `key` parameter names (see useOpenParam). */
const PAGES = {
  invoice: { page: "invoices", key: "invoice" },
  customer: { page: "customers", key: "customer" },
  subscription: { page: "subscriptions", key: "subscription" },
  payment: { page: "payments", key: "payment" },
  paymentLink: { page: "payment-links", key: "link" },
} as const;

/** Where a link to a record goes: the record's list page, with the record's id in the parameter that opens its sheet. A person's pages are under /app/t, the operator's under /tenants. */
export function recordLink(kind: keyof typeof PAGES, id: string, tenantId = ""): { to: string; params: { id: string }; search: never } {
  const { page, key } = PAGES[kind];
  return { to: `${tenantBase()}/$id/${page}`, params: { id: tenantId }, search: { [key]: id } as never };
}
