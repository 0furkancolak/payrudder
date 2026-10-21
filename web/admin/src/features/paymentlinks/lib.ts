import { parseMajor } from "@/shared/lib/money";

export type LinkKind = "invoice" | "adhoc";

export interface LinkForm {
  kind: LinkKind;
  invoiceId: string;
  amount: string;
  currency: string;
  description: string;
  customer: string;
  singleUse: boolean;
  days: string;
  key: string;
}

/** A fresh idempotency key for one submission of the form: a retry of the same submission reuses it. */
export function newKey(): string {
  return crypto.randomUUID();
}

/** The API request for a form, or the name of the field that is wrong (a locale key under billing.paymentLinks.errors). */
export function toRequest(f: LinkForm): { body: Record<string, unknown> } | { error: "invoice" | "amount" | "description" | "days" } {
  const days = Number(f.days);
  if (!Number.isInteger(days) || days < 1 || days > 365) return { error: "days" };
  const common = { kind: f.kind, single_use: f.singleUse, expires_in_days: days, idempotency_key: f.key };
  if (f.kind === "invoice") {
    if (!f.invoiceId.trim()) return { error: "invoice" };
    return { body: { ...common, invoice_id: f.invoiceId.trim() } };
  }
  const minor = parseMajor(f.amount, f.currency);
  if (minor === null || minor === "0") return { error: "amount" };
  if (!f.description.trim()) return { error: "description" };
  return { body: { ...common, amount_minor: minor, currency: f.currency, description: f.description.trim(), ...(f.customer.trim() ? { customer_external_id: f.customer.trim() } : {}) } };
}

/** A link the gateway returned relative ("/i/abc") becomes absolute against the console's origin. */
export function absoluteUrl(url: string | undefined, origin: string): string {
  if (!url) return "";
  return url.startsWith("/") ? origin + url : url;
}

export function statusVariant(status: string | undefined): "success" | "secondary" | "destructive" {
  if (status === "paid") return "success";
  if (status === "revoked" || status === "expired") return "destructive";
  return "secondary";
}
