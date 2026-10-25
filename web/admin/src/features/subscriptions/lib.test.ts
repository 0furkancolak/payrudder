import { describe, expect, it } from "vitest";
import { changeOutcome, intervalKey, subscriptionAbilities } from "./lib";

describe("subscription helpers", () => {
  it("names an interval, monthly unless it is a year", () => {
    expect(intervalKey("SUBSCRIPTION_INTERVAL_YEAR")).toBe("yearly");
    expect(intervalKey("SUBSCRIPTION_INTERVAL_MONTH")).toBe("monthly");
    expect(intervalKey(undefined)).toBe("monthly");
  });

  it("reads the net of a quote as nothing, a charge or a credit", () => {
    expect(changeOutcome(undefined)).toEqual({ kind: "nothing" });
    expect(changeOutcome("0")).toEqual({ kind: "nothing" });
    expect(changeOutcome("1250")).toEqual({ kind: "due", minor: "1250" });
    expect(changeOutcome("-300")).toEqual({ kind: "credit", minor: "300" });
  });

  it("works out what can be done with a subscription", () => {
    const active = { owner: "quotify", fee_timing: "FEE_TIMING_ADVANCE", state: "SUBSCRIPTION_STATE_ACTIVE" } as const;
    expect(subscriptionAbilities(active)).toEqual({ providerRuns: false, manageHere: true, advanceActions: true, canEndWithCredit: true });
    expect(subscriptionAbilities({ ...active, coupon_code: "SPRING" }).canEndWithCredit).toBe(false);
    expect(subscriptionAbilities({ ...active, end_time: "2026-10-01T00:00:00Z" })).toEqual({ providerRuns: false, manageHere: false, advanceActions: false, canEndWithCredit: false });
    expect(subscriptionAbilities({ ...active, owner: "provider" })).toMatchObject({ providerRuns: true, manageHere: false, advanceActions: false });
    expect(subscriptionAbilities({ ...active, fee_timing: "FEE_TIMING_ARREARS" })).toMatchObject({ manageHere: true, advanceActions: false, canEndWithCredit: false });
  });
});
