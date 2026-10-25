import { describe, expect, it } from "vitest";
import { CREDITS_OR_ZERO_RE, CREDITS_RE, validLinkDays } from "./rules";

describe("rules the console checks before sending", () => {
  it("accepts 1 to 365 whole days for a link", () => {
    expect([1, 90, 365].every(validLinkDays)).toBe(true);
    expect([0, 366, 1.5, Number.NaN, -3].some(validLinkDays)).toBe(false);
  });

  it("reads credits as a positive whole number of at most twelve digits", () => {
    expect(CREDITS_RE.test("1")).toBe(true);
    expect(CREDITS_RE.test("999999999999")).toBe(true);
    expect(CREDITS_RE.test("0")).toBe(false);
    expect(CREDITS_RE.test("01")).toBe(false);
    expect(CREDITS_RE.test("1000000000000")).toBe(false);
    expect(CREDITS_RE.test("1.5")).toBe(false);
  });

  it("lets a threshold be zero", () => {
    expect(CREDITS_OR_ZERO_RE.test("0")).toBe(true);
    expect(CREDITS_OR_ZERO_RE.test("12")).toBe(true);
    expect(CREDITS_OR_ZERO_RE.test("-1")).toBe(false);
  });
});
