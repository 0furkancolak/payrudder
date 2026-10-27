import { describe, expect, it } from "vitest";
import { changeOutcome, intervalKey, MAX_SEATS, parseSeats, seatsOf, subscriptionAbilities, successorOf, trialDaysLeft, trialsEndingSoon } from "./lib";

describe("seats", () => {
  it("reads a seats field: empty is nothing chosen, a whole number in range is a number, anything else is bad", () => {
    expect(parseSeats("")).toBeNull();
    expect(parseSeats("  ")).toBeNull();
    expect(parseSeats(undefined)).toBeNull();
    expect(parseSeats("5")).toBe(5);
    expect(parseSeats(" 12 ")).toBe(12);
    expect(parseSeats(3)).toBe(3);
    expect(parseSeats(String(MAX_SEATS))).toBe(MAX_SEATS);
    for (const bad of ["0", "-1", "1.5", "1e3", "abc", String(MAX_SEATS + 1), "0000001000000"]) expect(parseSeats(bad)).toBe("bad");
  });

  it("a subscription without the field has one seat", () => {
    expect(seatsOf({})).toBe(1);
    expect(seatsOf({ quantity: 0 })).toBe(1);
    expect(seatsOf({ quantity: 7 })).toBe(7);
  });
});

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
    const active = { owner: "payrudder", fee_timing: "FEE_TIMING_ADVANCE", state: "SUBSCRIPTION_STATE_ACTIVE" } as const;
    expect(subscriptionAbilities(active)).toEqual({ providerRuns: false, manageHere: true, advanceActions: true, canEndWithCredit: true });
    expect(subscriptionAbilities({ ...active, coupon_code: "SPRING" }).canEndWithCredit).toBe(false);
    expect(subscriptionAbilities({ ...active, end_time: "2026-10-01T00:00:00Z" })).toEqual({ providerRuns: false, manageHere: false, advanceActions: false, canEndWithCredit: false });
    expect(subscriptionAbilities({ ...active, owner: "provider" })).toMatchObject({ providerRuns: true, manageHere: false, advanceActions: false });
    expect(subscriptionAbilities({ ...active, fee_timing: "FEE_TIMING_ARREARS" })).toMatchObject({ manageHere: true, advanceActions: false, canEndWithCredit: false });
  });
});

describe("trials", () => {
  const now = new Date("2026-10-26T10:00:00Z");
  const trial = (end: string) => ({ state: "SUBSCRIPTION_STATE_TRIALING" as const, trial_end_time: end });

  it("counts the days left in a trial, rounded up, and knows nothing of a subscription that is not trialing", () => {
    expect(trialDaysLeft(trial("2026-10-29T09:00:00Z"), now)).toBe(3);
    expect(trialDaysLeft(trial("2026-10-26T12:00:00Z"), now)).toBe(1);
    expect(trialDaysLeft(trial("2026-10-25T12:00:00Z"), now)).toBe(0);
    expect(trialDaysLeft({ state: "SUBSCRIPTION_STATE_ACTIVE", trial_end_time: "2026-10-29T09:00:00Z" }, now)).toBeNull();
    expect(trialDaysLeft({ state: "SUBSCRIPTION_STATE_TRIALING" }, now)).toBeNull();
  });

  it("lists the trials that end within a week, soonest first", () => {
    const list: { state: "SUBSCRIPTION_STATE_TRIALING" | "SUBSCRIPTION_STATE_ACTIVE"; trial_end_time?: string }[] = [trial("2026-11-20T00:00:00Z"), trial("2026-10-30T00:00:00Z"), trial("2026-10-28T00:00:00Z"), { state: "SUBSCRIPTION_STATE_ACTIVE" as const }];
    expect(trialsEndingSoon(list, now).map((s) => s.trial_end_time)).toEqual(["2026-10-28T00:00:00Z", "2026-10-30T00:00:00Z"]);
    expect(trialsEndingSoon(list, now, 30)).toHaveLength(3);
  });
});

describe("successorOf", () => {
  const list = [{ id: "a" }, { id: "b", changed_from_id: "a" }, { id: "c", changed_from_id: "" }];
  it("finds the subscription that took over from another", () => {
    expect(successorOf(list, "a")?.id).toBe("b");
  });
  it("is nothing for a subscription nobody replaced, or no id", () => {
    expect(successorOf(list, "b")).toBeUndefined();
    expect(successorOf(list, "")).toBeUndefined();
    expect(successorOf(list, undefined)).toBeUndefined();
  });
});
