import type { components } from "@/shared/api/schema";

export type InvoiceTransfer = components["schemas"]["InvoiceTransfer"];
export type TransferReceiptResult = components["schemas"]["TransferReceiptResult"];
export type RecordInvoicePaymentResult = components["schemas"]["RecordInvoicePaymentResult"];
export type InvoicePayment = components["schemas"]["InvoicePayment"];

/** The methods an offline payment can be recorded with. */
export const PAYMENT_METHODS = ["bank_transfer", "cash", "other"] as const;
export type PaymentMethod = (typeof PAYMENT_METHODS)[number];

/** The operator label sent as X-Quotify-Actor: the auth feature knows only the shared operator token, so there is no name to send. */
export const ACTOR = "console";

/** Invoices that can still be paid by hand. */
export function canRecordPayment(status: string | undefined): boolean {
  return status === "open" || status === "uncollectible";
}

/** Whether the bank transfer state of an invoice is worth asking for. */
export function wantsTransferState(status: string | undefined, sagaState: string | undefined): boolean {
  return canRecordPayment(status) || sagaState === "PAYMENT_PENDING";
}

/**
 * Whether the invoice was charged by bank transfer and money is still expected or was given up on (so "Record payment" confirms a transfer
 * through the transfer endpoint). `none` or no information at all means the offline payment endpoint.
 */
export function isTransferCharge(transfer: InvoiceTransfer | undefined | null, invoiceStatus?: string): boolean {
  const s = transfer?.state;
  // An uncollectible invoice is past the saga's waiting: a payment is recorded on the invoice (which settles it), not confirmed through the transfer.
  return invoiceStatus !== "uncollectible" && (s === "pending" || s === "expired" || s === "rejected");
}

/** What is left of an invoice after the payments recorded by hand, in minor units (never below zero). Done with BigInt: totals are int64. */
export function outstandingAfter(totalMinor: string | undefined, payments: { amount_minor?: string }[] | undefined): string {
  if (!totalMinor || !/^\d+$/.test(totalMinor)) return "0";
  let left = BigInt(totalMinor);
  for (const p of payments ?? []) if (p.amount_minor && /^\d+$/.test(p.amount_minor)) left -= BigInt(p.amount_minor);
  return (left < 0n ? 0n : left).toString();
}

const two = (n: number) => String(n).padStart(2, "0");

/** A date as the value of an <input type="datetime-local"> in the browser's time zone ("2026-10-20T14:05"). */
export function toLocalInput(d: Date): string {
  return `${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())}T${two(d.getHours())}:${two(d.getMinutes())}`;
}

/** The RFC 3339 instant for what the operator picked, or null when it is not a date or lies after `now` (the server refuses both). */
export function receivedAtToIso(local: string, now: Date): string | null {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/.test(local)) return null;
  const at = new Date(local);
  if (Number.isNaN(at.getTime()) || at.getTime() > now.getTime()) return null;
  return at.toISOString();
}

/** What a recorded payment did to the invoice, in the terms the toast needs. Amounts are minor units as strings. */
export interface Settlement {
  paid: boolean;
  duplicate: boolean;
  received: string;
  outstanding: string;
}

export function transferSettlement(r: TransferReceiptResult): Settlement {
  return {
    paid: r.paid === true,
    duplicate: r.duplicate === true,
    received: String(r.transfer?.received_minor ?? r.receipt?.amount_minor ?? 0),
    outstanding: String(r.transfer?.outstanding_minor ?? 0),
  };
}

export function offlineSettlement(r: RecordInvoicePaymentResult): Settlement {
  return { paid: r.settled === true, duplicate: r.duplicate === true, received: r.paid_minor ?? "0", outstanding: r.outstanding_minor ?? "0" };
}
