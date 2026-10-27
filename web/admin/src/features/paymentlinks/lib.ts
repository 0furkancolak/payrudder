import type { components } from "@/shared/api/schema";
import { parseMajor } from "@/shared/lib/money";
import { validLinkDays } from "@/shared/lib/rules";

/** The body of POST /v1/payment-links. */
export type CreateLinkBody = components["schemas"]["CreatePaymentLinkRequest"];

type LinkKind = "invoice" | "adhoc";

export interface LinkForm {
  kind: LinkKind;
  invoiceId: string;
  amount: string;
  currency: string;
  description: string;
  customer: string;
  /** A promotion code the payer gets without typing it (optional, ad hoc links). */
  promotion: string;
  singleUse: boolean;
  days: string;
  key: string;
}

/** The API request for a form, or the name of the field that is wrong (a locale key under billing.paymentLinks.errors). */
export function toRequest(f: LinkForm): { body: CreateLinkBody } | { error: "invoice" | "amount" | "description" | "days" } {
  const days = Number(f.days);
  if (!validLinkDays(days)) return { error: "days" };
  const common = { kind: f.kind, single_use: f.singleUse, expires_in_days: days, idempotency_key: f.key };
  if (f.kind === "invoice") {
    if (!f.invoiceId.trim()) return { error: "invoice" };
    return { body: { ...common, invoice_id: f.invoiceId.trim() } };
  }
  const minor = parseMajor(f.amount, f.currency);
  if (minor === null || minor === "0") return { error: "amount" };
  if (!f.description.trim()) return { error: "description" };
  return { body: { ...common, amount_minor: minor, currency: f.currency, description: f.description.trim(), ...(f.customer.trim() ? { customer_external_id: f.customer.trim() } : {}), ...(f.promotion.trim() ? { promotion_code: f.promotion.trim() } : {}) } };
}

export { absoluteUrl } from "@/shared/lib/url";

export function statusVariant(status: string | undefined): "success" | "secondary" | "destructive" {
  if (status === "paid") return "success";
  if (status === "revoked" || status === "expired") return "destructive";
  return "secondary";
}
