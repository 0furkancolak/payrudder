import { describe, expect, it } from "vitest";
import { branded } from "@/shared/lib/brand";

describe("branded", () => {
  it("replaces the default name in every text, nested or not", () => {
    expect(branded({ a: "Payrudder console", b: { c: ["Payrudder's copy", 3, null] } }, "Acme Billing")).toEqual({
      a: "Acme Billing console",
      b: { c: ["Acme Billing's copy", 3, null] },
    });
  });

  it("leaves the messages alone when the name is the default", () => {
    const m = { a: "Payrudder" };
    expect(branded(m, "Payrudder")).toBe(m);
  });
});
