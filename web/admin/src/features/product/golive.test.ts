import { describe, expect, it } from "vitest";
import { GO_LIVE_ITEMS, SCENARIOS, checklist, nextStep, progress } from "./golive";

const full = { items: GO_LIVE_ITEMS.map((key) => ({ key, done: true })) };

describe("the go-live checklist", () => {
  it("lists the five items in order and counts the done ones", () => {
    const g = { items: [{ key: "email_verified", done: true }, { key: "legal_profile", done: false }, { key: "payment_provider", done: false }, { key: "plan_published", done: true }, { key: "terms_accepted", done: false }] } as const;
    expect(checklist(g as never).map((i) => i.key)).toEqual([...GO_LIVE_ITEMS]);
    expect(progress(g as never)).toEqual({ done: 2, total: 5 });
    expect(nextStep(g as never)).toBe("legal_profile");
  });

  it("never reads a short or missing answer as complete", () => {
    expect(progress(undefined)).toEqual({ done: 0, total: 5 });
    expect(progress({ items: [{ key: "email_verified", done: true }] } as never)).toEqual({ done: 1, total: 5 });
    expect(nextStep({ items: [] } as never)).toBe("email_verified");
  });

  it("has no next step when everything is done, and ignores a key it does not know", () => {
    expect(nextStep(full as never)).toBeNull();
    expect(progress({ items: [...full.items, { key: "something_new", done: false }] } as never)).toEqual({ done: 5, total: 5 });
  });
});

describe("the scenario catalogue", () => {
  it("has six scenarios, one default, with distinct names", () => {
    expect(SCENARIOS).toHaveLength(6);
    expect(SCENARIOS.filter((s) => "default" in s)).toHaveLength(1);
    expect(new Set(SCENARIOS.map((s) => s.scenario)).size).toBe(6);
  });
});
