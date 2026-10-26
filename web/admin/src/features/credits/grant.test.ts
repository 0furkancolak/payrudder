import { describe, expect, it } from "vitest";
import { expiryTime, reasonText, sourceOf } from "./grant";

describe("free credits", () => {
  it("keeps a stable English reason in the ledger, with the person's note", () => {
    expect(reasonText("welcome", "")).toBe("Welcome gift");
    expect(reasonText("goodwill", " late invoice ")).toBe("Goodwill: late invoice");
    expect(reasonText("other", "")).toBeUndefined();
    expect(reasonText("other", "x".repeat(300))?.length).toBe(200);
  });

  it("makes promotional lots for gifts and promotions, manual ones for the rest", () => {
    expect([sourceOf("welcome"), sourceOf("promotion"), sourceOf("goodwill"), sourceOf("other")]).toEqual(["promo", "promo", "manual", "manual"]);
  });

  it("works out when they run out", () => {
    const now = new Date(2026, 9, 26, 10, 0, 0);
    expect(expiryTime("never", "", now)).toBeNull();
    const in30 = new Date(expiryTime("30", "", now) as string);
    expect([in30.getFullYear(), in30.getMonth(), in30.getDate(), in30.getHours()]).toEqual([2026, 10, 25, 23]);
    const onDate = new Date(expiryTime("date", "2026-12-31", now) as string);
    expect([onDate.getMonth(), onDate.getDate(), onDate.getHours()]).toEqual([11, 31, 23]);
  });

  it("refuses a date that is missing, malformed or not in the future", () => {
    const now = new Date(2026, 9, 26, 10, 0, 0);
    expect(expiryTime("date", "", now)).toBeUndefined();
    expect(expiryTime("date", "26/10/2026", now)).toBeUndefined();
    expect(expiryTime("date", "2026-10-25", now)).toBeUndefined();
  });
});
