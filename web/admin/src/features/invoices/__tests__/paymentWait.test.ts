import { describe, expect, it } from "vitest";
import { ACTION_REQUIRED, HELD, heldActions, holdReason, releaseOutcome, waits } from "../paymentWait";

describe("what an invoice's payment waits for", () => {
  it("is worth asking only while the saga holds the first charge or waits for the customer", () => {
    expect(waits(HELD)).toBe(true);
    expect(waits(ACTION_REQUIRED)).toBe(true);
    for (const s of ["PAYMENT_DECLINED", "PAYMENT_PENDING", "PAYMENT_REQUESTED", "COMPLETED", "VOIDED", undefined]) expect(waits(s)).toBe(false);
  });

  it("names the reason the payment gate gave, and 'unknown' when it gave none", () => {
    expect(holdReason({ hold_reason: "customer_blocked" })).toBe("customer_blocked");
    expect(holdReason({ hold_reason: "tenant_on_hold" })).toBe("tenant_on_hold");
    expect(holdReason({ hold_reason: "none" })).toBe("none");
    expect(holdReason({})).toBe("unknown");
  });

  it("offers release and void to somebody who may write, while the seller can change what holds the charge", () => {
    expect(heldActions({ state: HELD, hold_reason: "customer_blocked" }, true)).toEqual({ release: true, void: true });
    expect(heldActions({ state: HELD, hold_reason: "none" }, true)).toEqual({ release: true, void: true });
    expect(heldActions({ state: HELD }, true)).toEqual({ release: true, void: true }); // the gate could not be asked: the server decides
  });

  it("offers a viewer nothing, nor anybody while the operator has the environment on hold, nor a payment that is not held", () => {
    expect(heldActions({ state: HELD, hold_reason: "customer_blocked" }, false)).toEqual({ release: false, void: false });
    expect(heldActions({ state: HELD, hold_reason: "tenant_on_hold" }, true)).toEqual({ release: false, void: false });
    expect(heldActions({ state: ACTION_REQUIRED }, true)).toEqual({ release: false, void: false });
  });

  it("says what a release did from the state it answers", () => {
    expect(releaseOutcome("PAYMENT_REQUESTED")).toBe("released");
    expect(releaseOutcome(HELD)).toBe("stillHeld");
  });
});
