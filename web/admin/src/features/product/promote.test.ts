import { describe, expect, it } from "vitest";
import { actionOf, promoteSummary } from "./promote";

describe("promote summary", () => {
  it("maps the API's action words, and an unknown word is never a write", () => {
    expect(actionOf("ACTION_CREATE")).toBe("create");
    expect(actionOf("ACTION_CONFLICT")).toBe("conflict");
    expect(actionOf("ACTION_SKIPPED")).toBe("skipped");
    expect(actionOf("ACTION_UNCHANGED")).toBe("unchanged");
    expect(actionOf("ACTION_DELETE")).toBe("unchanged");
    expect(actionOf(undefined)).toBe("unchanged");
  });

  it("lists what would change and counts what would not", () => {
    const s = promoteSummary({
      created: 2,
      unchanged: 3,
      conflicts: 1,
      changes: [
        { family: "meters", key: "gb", action: "ACTION_CREATE" },
        { family: "plans", key: "pro", action: "ACTION_UNCHANGED" },
        { family: "taxes", key: "TR", action: "ACTION_CONFLICT", detail: "rate differs" },
        { family: "webhooks", key: "https://x.test/h", action: "ACTION_SKIPPED" },
      ],
    });
    expect(s.created).toBe(2);
    expect(s.rows.map((r) => r.action)).toEqual(["create", "conflict", "skipped"]);
    expect(s.nothing).toBe(false);
  });

  it("says nothing to do for an empty or all-unchanged diff, and keeps a failed row", () => {
    expect(promoteSummary(undefined).nothing).toBe(true);
    expect(promoteSummary({ changes: [{ family: "plans", key: "pro", action: "ACTION_UNCHANGED" }] }).nothing).toBe(true);
    expect(promoteSummary({ failed: 1, changes: [{ family: "plans", key: "pro", action: "ACTION_CREATE", error: "boom" }] }).rows[0]?.error).toBe("boom");
  });
});
