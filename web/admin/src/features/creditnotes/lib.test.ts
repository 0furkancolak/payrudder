import { describe, expect, it } from "vitest";
import { creditAmountMinor } from "./lib";

describe("creditAmountMinor", () => {
  it("credits what is left for an empty field", () => {
    expect(creditAmountMinor("", "USD")).toBe("0");
    expect(creditAmountMinor("  ", undefined)).toBe("0");
  });

  it("reads the amount with the exponent of the invoice's currency", () => {
    expect(creditAmountMinor("12.50", "USD")).toBe("1250");
    expect(creditAmountMinor("500", "JPY")).toBe("500");
    expect(creditAmountMinor("1,250", "KWD")).toBe("1250");
  });

  it("refuses zero, nonsense and too many decimals", () => {
    expect(creditAmountMinor("0", "USD")).toBeNull();
    expect(creditAmountMinor("abc", "USD")).toBeNull();
    expect(creditAmountMinor("1.234", "USD")).toBeNull();
  });

  it("refuses to guess the currency (the old fallback read yen as dollars)", () => {
    expect(creditAmountMinor("500", undefined)).toBeNull();
    expect(creditAmountMinor("500", "")).toBeNull();
  });
});
