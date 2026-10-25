import { describe, expect, it } from "vitest";
import { badCodes, codesCsv, emptyCodeForm, list, splitCodes, targetKey, toTemplate } from "./promotions";

describe("splitCodes", () => {
  it("splits on lines, commas and spaces, and counts a code once in any case", () => {
    expect(splitCodes("SUMMER26\nwelcome10, Summer26 ;  FREE-1")).toEqual(["SUMMER26", "welcome10", "FREE-1"]);
    expect(splitCodes("   \n ")).toEqual([]);
  });
  it("finds the codes the server would refuse", () => {
    expect(badCodes(["OK_1", "a", "no way", "x".repeat(41), "ünï"])).toEqual(["a", "no way", "x".repeat(41), "ünï"]);
  });
});

describe("codesCsv", () => {
  it("is a header and one code per line", () => {
    expect(codesCsv(["AB", "CD"])).toBe("code\r\nAB\r\nCD\r\n");
  });
});

describe("list", () => {
  it("keeps what was typed and nothing when it is empty", () => {
    expect(list(" a, b ;c\n")).toEqual(["a", "b", "c"]);
    expect(list("  ")).toBeUndefined();
  });
});

describe("toTemplate", () => {
  const now = new Date("2026-10-25T10:00:00Z");
  it("is empty for an empty form", () => {
    expect(toTemplate(emptyCodeForm("USD"), now)).toEqual({ template: {} });
  });
  it("reads every rule", () => {
    const r = toTemplate(
      { max: "5", expires: "2026-12-31", firstPurchase: true, onePerCustomer: true, minAmount: "12.50", minCurrency: "USD", currencies: "usd, eur", plans: "pro, team", packs: "p1", restore: true },
      now,
    );
    expect(r).toMatchObject({
      template: {
        max_redemptions: 5,
        first_purchase_only: true,
        one_per_customer: true,
        min_amount_minor: "1250",
        min_amount_currency: "USD",
        allowed_currencies: ["USD", "EUR"],
        allowed_plans: ["pro", "team"],
        allowed_packs: ["p1"],
        restore_on_reversal: true,
      },
    });
    expect("template" in r && typeof r.template.expire_time).toBe("string");
  });
  it("names the field that is wrong", () => {
    const f = emptyCodeForm("USD");
    expect(toTemplate({ ...f, max: "0" }, now)).toEqual({ error: "max" });
    expect(toTemplate({ ...f, max: "1.5" }, now)).toEqual({ error: "max" });
    expect(toTemplate({ ...f, expires: "2026-10-01" }, now)).toEqual({ error: "expires" });
    expect(toTemplate({ ...f, minAmount: "abc" }, now)).toEqual({ error: "minAmount" });
    expect(toTemplate({ ...f, minAmount: "0" }, now)).toEqual({ error: "minAmount" });
    expect(toTemplate({ ...f, currencies: "usd, euro" }, now)).toEqual({ error: "currencies" });
  });
});

describe("targetKey", () => {
  it("shortens the API names", () => {
    expect(targetKey("COUPON_TARGET_CREDIT_PURCHASE")).toBe("credit_purchase");
    expect(targetKey(undefined)).toBe("");
  });
});
