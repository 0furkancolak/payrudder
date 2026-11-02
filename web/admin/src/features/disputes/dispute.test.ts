import { describe, expect, it } from "vitest";
import { disputeKind, disputeTone, dueShown, dueState, meaningKey, needsHandling } from "./dispute";

describe("a dispute as the console words it", () => {
  it("tones the outcome: open waits, won is over, lost is money gone", () => {
    expect(["open", "won", "lost", "?"].map(disputeTone)).toEqual(["warning", "success", "danger", "neutral"]);
  });

  it("tells a chargeback from an inquiry", () => {
    expect(disputeKind({ formal: true })).toBe("chargeback");
    expect(disputeKind({ formal: false })).toBe("inquiry");
  });

  it("has one meaning per state of a case", () => {
    expect(meaningKey({ formal: true, outcome: "open" })).toBe("openChargeback");
    expect(meaningKey({ formal: false, outcome: "open" })).toBe("openInquiry");
    expect(meaningKey({ formal: true, outcome: "won" })).toBe("wonChargeback");
    expect(meaningKey({ formal: false, outcome: "won" })).toBe("wonInquiry");
    expect(meaningKey({ formal: true, outcome: "lost" })).toBe("lost");
  });

  it("shows the evidence date only while the case is open", () => {
    const at = "2026-11-20T00:00:00Z";
    expect(dueShown({ outcome: "open", evidence_due_time: at })).toBe(at);
    expect(dueShown({ outcome: "won", evidence_due_time: at })).toBeUndefined();
    expect(dueShown({ outcome: "open" })).toBeUndefined();
  });

  it("needs handling only while it is open and the seller has not marked it", () => {
    expect(needsHandling({ outcome: "open", handled: false })).toBe(true);
    expect(needsHandling({ outcome: "open", handled: true })).toBe(false);
    expect(needsHandling({ outcome: "won", handled: false })).toBe(false);
  });

  it("says how close the evidence date is, and nothing for a case that is closed, handled or has no date", () => {
    const now = new Date("2026-11-10T12:00:00Z");
    const open = (at: string | undefined, handled = false) => ({ outcome: "open" as const, evidence_due_time: at, handled });
    expect(dueState(open("2026-11-09T12:00:00Z"), now)).toBe("overdue");
    expect(dueState(open("2026-11-12T12:00:00Z"), now)).toBe("soon");
    expect(dueState(open("2026-11-13T12:00:00Z"), now)).toBe("soon"); // exactly three days
    expect(dueState(open("2026-11-13T12:00:01Z"), now)).toBe("later");
    expect(dueState(open("2026-11-09T12:00:00Z", true), now)).toBeUndefined();
    expect(dueState({ outcome: "won" as const, evidence_due_time: "2026-11-09T12:00:00Z", handled: false }, now)).toBeUndefined();
    expect(dueState(open(undefined), now)).toBeUndefined();
    expect(dueState(open("not a date"), now)).toBeUndefined();
  });
});
