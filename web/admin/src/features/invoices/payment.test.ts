import { describe, expect, it } from "vitest";
import {
  canRecordPayment,
  isTransferCharge,
  offlineSettlement,
  outstandingAfter,
  receivedAtToIso,
  toLocalInput,
  transferSettlement,
  wantsTransferState,
  type InvoiceTransfer,
  type RecordInvoicePaymentResult,
  type TransferReceiptResult,
} from "./payment";

type TransferReceipt = TransferReceiptResult["receipt"];

const receipt = (o: Partial<TransferReceipt> = {}): TransferReceipt => ({
  id: 1,
  amount_minor: 0,
  currency: "TRY",
  received_at: "2026-10-20T09:15:00Z",
  bank_reference: "REF-1",
  recorded_by: "console",
  recorded_at: "2026-10-20T09:16:00Z",
  ...o,
});
const transfer = (o: Partial<InvoiceTransfer> = {}): InvoiceTransfer => ({ invoice_id: "inv_1", state: "pending", received_minor: 0, receipts: [], ...o });
const transferResult = (o: Partial<TransferReceiptResult> = {}): TransferReceiptResult => ({ receipt: receipt(), duplicate: false, paid: false, saga_notified: false, transfer: transfer(), ...o });
const offlineResult = (o: Partial<RecordInvoicePaymentResult> = {}): RecordInvoicePaymentResult => ({ payment: null, duplicate: false, paid_minor: "0", outstanding_minor: "0", settled: false, ...o });

describe("which invoices", () => {
  it("can be paid by hand while open or uncollectible", () => {
    expect(canRecordPayment("open")).toBe(true);
    expect(canRecordPayment("uncollectible")).toBe(true);
    for (const s of ["paid", "void", "draft", undefined]) expect(canRecordPayment(s)).toBe(false);
  });
  it("ask for the transfer state when payable or waiting for a transfer", () => {
    expect(wantsTransferState("open", "PAYMENT_DECLINED")).toBe(true);
    expect(wantsTransferState("paid", "PAYMENT_PENDING")).toBe(true);
    expect(wantsTransferState("paid", "COMPLETED")).toBe(false);
  });
  it("were charged by transfer unless the state is none, paid or unknown", () => {
    expect(isTransferCharge(transfer({ state: "pending" }))).toBe(true);
    expect(isTransferCharge(transfer({ state: "expired" }))).toBe(true);
    expect(isTransferCharge(transfer({ state: "rejected" }))).toBe(true);
    expect(isTransferCharge(transfer({ state: "none" }))).toBe(false);
    expect(isTransferCharge(transfer({ state: "paid" }))).toBe(false);
    expect(isTransferCharge(undefined)).toBe(false);
    // Once the invoice is uncollectible a late transfer is recorded on the invoice, which settles it.
    expect(isTransferCharge(transfer({ state: "expired" }), "uncollectible")).toBe(false);
    expect(isTransferCharge(transfer({ state: "pending" }), "open")).toBe(true);
    expect(isTransferCharge(null)).toBe(false);
  });
});

describe("outstandingAfter", () => {
  it("subtracts the recorded payments exactly", () => {
    expect(outstandingAfter("10000", [{ amount_minor: "2500" }, { amount_minor: "2500" }])).toBe("5000");
    expect(outstandingAfter("9007199254740993", [{ amount_minor: "1" }])).toBe("9007199254740992");
    expect(outstandingAfter("500", undefined)).toBe("500");
  });
  it("never goes below zero and ignores what is not a number", () => {
    expect(outstandingAfter("100", [{ amount_minor: "150" }])).toBe("0");
    expect(outstandingAfter("100", [{ amount_minor: "x" }, {}])).toBe("100");
    expect(outstandingAfter(undefined, [])).toBe("0");
  });
});

describe("receivedAtToIso", () => {
  const now = new Date(2026, 9, 20, 14, 30); // local time
  it("turns a picked local time into an instant", () => {
    expect(receivedAtToIso("2026-10-20T09:15", now)).toBe(new Date(2026, 9, 20, 9, 15).toISOString());
    expect(receivedAtToIso(toLocalInput(now), now)).toBe(now.toISOString());
  });
  it("refuses the future and non-dates", () => {
    expect(receivedAtToIso("2026-10-20T14:31", now)).toBeNull();
    expect(receivedAtToIso("2027-01-01T00:00", now)).toBeNull();
    expect(receivedAtToIso("", now)).toBeNull();
    expect(receivedAtToIso("yesterday", now)).toBeNull();
    expect(receivedAtToIso("2026-13-40T10:00", now)).toBeNull();
  });
});

describe("settlements", () => {
  it("reads a confirmed transfer", () => {
    expect(transferSettlement(transferResult({ receipt: receipt({ amount_minor: 4000 }), transfer: transfer({ received_minor: 4000, outstanding_minor: 6000 }) }))).toEqual({
      paid: false,
      duplicate: false,
      received: "4000",
      outstanding: "6000",
    });
    expect(transferSettlement(transferResult({ paid: true, duplicate: true, transfer: transfer({ received_minor: 10000, outstanding_minor: 0 }) })).paid).toBe(true);
  });
  it("reads an offline payment", () => {
    expect(offlineSettlement(offlineResult({ paid_minor: "1000", outstanding_minor: "9000" }))).toEqual({ paid: false, duplicate: false, received: "1000", outstanding: "9000" });
    expect(offlineSettlement(offlineResult({ settled: true, paid_minor: "10000", outstanding_minor: "0" })).paid).toBe(true);
  });
});
