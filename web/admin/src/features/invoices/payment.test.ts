import { describe, expect, it } from "vitest";
import { canRecordPayment, isTransferCharge, offlineSettlement, outstandingAfter, receivedAtToIso, toLocalInput, transferSettlement, wantsTransferState } from "./payment";

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
    expect(isTransferCharge({ state: "pending" })).toBe(true);
    expect(isTransferCharge({ state: "expired" })).toBe(true);
    expect(isTransferCharge({ state: "rejected" })).toBe(true);
    expect(isTransferCharge({ state: "none" })).toBe(false);
    expect(isTransferCharge({ state: "paid" })).toBe(false);
    expect(isTransferCharge(undefined)).toBe(false);
    // Once the invoice is uncollectible a late transfer is recorded on the invoice, which settles it.
    expect(isTransferCharge({ state: "expired" }, "uncollectible")).toBe(false);
    expect(isTransferCharge({ state: "pending" }, "open")).toBe(true);
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
    expect(transferSettlement({ paid: false, receipt: { amount_minor: 4000 }, transfer: { received_minor: 4000, outstanding_minor: 6000 } })).toEqual({
      paid: false,
      duplicate: false,
      received: "4000",
      outstanding: "6000",
    });
    expect(transferSettlement({ paid: true, duplicate: true, transfer: { received_minor: 10000, outstanding_minor: 0 } }).paid).toBe(true);
  });
  it("reads an offline payment", () => {
    expect(offlineSettlement({ settled: false, paid_minor: "1000", outstanding_minor: "9000" })).toEqual({ paid: false, duplicate: false, received: "1000", outstanding: "9000" });
    expect(offlineSettlement({ settled: true, paid_minor: "10000", outstanding_minor: "0" }).paid).toBe(true);
  });
});
