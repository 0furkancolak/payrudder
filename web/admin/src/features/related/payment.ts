import type { Payment } from "@/features/product/billingApi";
import { formatMinor } from "@/shared/lib/money";

/** A payment's amount as the lists and sheets show it: a refund is negative. */
export const signedAmount = (p: Payment, locale: string) => formatMinor(p.status === "refunded" ? `-${p.amount_minor}` : p.amount_minor, p.currency, locale);
