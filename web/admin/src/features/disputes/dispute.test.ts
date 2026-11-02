import { describe, expect, it } from "vitest";
import { disputeKind, disputeTone, dueShown, meaningKey } from "./dispute";

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
});
