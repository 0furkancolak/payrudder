import { describe, expect, it } from "vitest";
import { actionable, environmentOf, recentPeriods, resolutionsFor, statusesOf } from "../reconcile";

describe("the reconciliation queue", () => {
  it("asks the server for what needs a person by default", () => {
    expect(statusesOf("review")).toEqual(["open", "explained"]);
    expect(statusesOf("resolved")).toEqual(["resolved"]);
    expect(statusesOf("all")).toEqual([]);
  });

  it("offers a ledger fact only for one paid-without-charge discrepancy at a time", () => {
    expect(resolutionsFor("paid_without_charge", false)).toContain("ledger_fact");
    expect(resolutionsFor("paid_without_charge", true)).not.toContain("ledger_fact");
    expect(resolutionsFor("amount_mismatch", false)).not.toContain("ledger_fact");
    expect(resolutionsFor("amount_mismatch", false)).toEqual(["credit_note", "refunded_at_provider", "corrected_in_billing", "not_an_error", "other"]);
  });

  it("leaves resolved ones alone", () => {
    expect(actionable({ status: "open" })).toBe(true);
    expect(actionable({ status: "explained" })).toBe(true);
    expect(actionable({ status: "resolved" })).toBe(false);
  });

  it("lists this month and the eleven before it across a year end", () => {
    const p = recentPeriods(new Date(Date.UTC(2026, 0, 15)));
    expect(p).toHaveLength(12);
    expect(p.slice(0, 3)).toEqual(["2026-01", "2025-12", "2025-11"]);
    expect(p[11]).toBe("2025-02");
  });

  it("names the environment the way the API does", () => {
    expect(environmentOf("ENVIRONMENT_LIVE")).toBe("live");
    expect(environmentOf("ENVIRONMENT_SANDBOX")).toBe("sandbox");
    expect(environmentOf(undefined)).toBeUndefined();
  });
});
