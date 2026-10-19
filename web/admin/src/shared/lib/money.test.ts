import { describe, expect, it } from "vitest";
import { formatMinor } from "./money";

describe("formatMinor", () => {
  it("uses the currency's minor-unit exponent", () => {
    expect(formatMinor("1000", "TRY", "en")).toMatch(/10\.00/);
    expect(formatMinor("1000", "JPY", "en")).toMatch(/1,000/);
    expect(formatMinor("1234", "KWD", "en")).toMatch(/1\.234/);
  });
  it("keeps a large amount exact", () => {
    expect(formatMinor("9007199254740993", "USD", "en")).toContain("90,071,992,547,409.93");
  });
  it("follows the locale", () => {
    expect(formatMinor("123456", "EUR", "de")).toContain("1.234,56");
  });
  it("passes through what it cannot format", () => {
    expect(formatMinor("12", undefined, "en")).toBe("12");
    expect(formatMinor("abc", "USD", "en")).toBe("abc");
  });
  it("handles negative amounts (credits)", () => {
    expect(formatMinor("-250", "USD", "en")).toContain("2.50");
    expect(formatMinor("-250", "USD", "en")).toContain("-");
  });
});
