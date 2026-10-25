import { describe, expect, it } from "vitest";
import { formatCredits, kindVariant, validityDays, validQuantity } from "./lib";

describe("credits", () => {
  it("formats int64 text without a float", () => {
    expect(formatCredits("9007199254740993", "en-US")).toBe("9,007,199,254,740,993");
    expect(formatCredits(1500, "en-US")).toBe("1,500");
    expect(formatCredits(undefined, "en-US")).toBe("");
    expect(formatCredits("", "en-US")).toBe("");
    expect(formatCredits("1.5", "en-US")).toBe("1.5");
  });

  it("badges a ledger kind", () => {
    expect(kindVariant("grant")).toBe("success");
    expect(kindVariant("expire")).toBe("secondary");
    expect(kindVariant("consume")).toBe("outline");
    expect(kindVariant(undefined)).toBe("outline");
  });

  it("reads the validity of a pack: empty means never, a number from 1 to the limit, anything else is wrong", () => {
    expect(validityDays("", 3650)).toBeNull();
    expect(validityDays(" 30 ", 3650)).toBe(30);
    expect(validityDays("3650", 3650)).toBe(3650);
    expect(validityDays("3651", 3650)).toBeUndefined();
    expect(validityDays("0", 3650)).toBeUndefined();
    expect(validityDays("12345", 3650)).toBeUndefined();
    expect(validityDays("ten", 3650)).toBeUndefined();
  });

  it("accepts a purchase quantity from 1 to the limit", () => {
    expect(validQuantity("1", 1000)).toBe(true);
    expect(validQuantity("1000", 1000)).toBe(true);
    expect(validQuantity("0", 1000)).toBe(false);
    expect(validQuantity("1001", 1000)).toBe(false);
    expect(validQuantity("2.5", 1000)).toBe(false);
    expect(validQuantity("", 1000)).toBe(false);
  });
});
