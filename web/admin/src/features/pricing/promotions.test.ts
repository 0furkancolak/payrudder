import { describe, expect, it } from "vitest";
import { badCodes, codesCsv, couponEffect, emptyCodeForm, emptyCouponForm, list, parseBonus, splitCodes, targetKey, toCouponBody, toTemplate, type CouponForm } from "./promotions";

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

describe("coupon form", () => {
  const form = (over: Partial<CouponForm> = {}): CouponForm => ({ ...emptyCouponForm("USD"), code: "WELCOME", value: "10", ...over });
  const now = new Date("2026-10-20T10:00:00");

  it("makes the request of a percentage coupon that works everywhere", () => {
    expect(toCouponBody(form(), now)).toEqual({ body: { code: "WELCOME", name: "", percent_bps: 1000, duration: "COUPON_DURATION_ONCE" } });
  });

  it("names where it works only when that is not everywhere, and gives a repeating coupon its number of fees", () => {
    const r = toCouponBody(form({ targets: ["COUPON_TARGET_SUBSCRIPTION"], duration: "COUPON_DURATION_REPEATING", periods: "3", value: "12,5" }), now);
    expect(r).toEqual({ body: { code: "WELCOME", name: "", percent_bps: 1250, applies_to: ["COUPON_TARGET_SUBSCRIPTION"], duration: "COUPON_DURATION_REPEATING", duration_periods: 3 } });
  });

  it("has no duration for a coupon that does not work on subscriptions, and a bonus coupon is for credit purchases", () => {
    const pay = toCouponBody(form({ targets: ["COUPON_TARGET_PAYMENT"] }), now);
    expect(pay).toEqual({ body: { code: "WELCOME", name: "", percent_bps: 1000, applies_to: ["COUPON_TARGET_PAYMENT"] } });
    expect(toCouponBody(form({ kind: "bonus", value: "25", targets: [] }), now)).toEqual({ body: { code: "WELCOME", name: "", bonus_percent_bps: 2500 } });
  });

  it("reads an amount in the currency it is in, with a limit, an expiry and no default code", () => {
    const r = toCouponBody(form({ kind: "amount", value: "4.99", currency: "USD", max: "100", expires: "2026-12-31", defaultCode: false, duration: "COUPON_DURATION_FOREVER" }), now);
    expect(r).toMatchObject({ body: { amount_minor: "499", currency: "USD", max_redemptions: 100, without_default_code: true, duration: "COUPON_DURATION_FOREVER" } });
    expect("body" in r && r.body.expire_time).toBe(new Date("2026-12-31T23:59:59").toISOString());
  });

  it("names the field that is wrong", () => {
    expect(toCouponBody(form({ code: "x" }), now)).toEqual({ error: "code" });
    expect(toCouponBody(form({ value: "101" }), now)).toEqual({ error: "value" });
    expect(toCouponBody(form({ value: "0" }), now)).toEqual({ error: "value" });
    expect(toCouponBody(form({ kind: "amount", value: "4.999" }), now)).toEqual({ error: "value" });
    expect(toCouponBody(form({ targets: [] }), now)).toEqual({ error: "targets" });
    expect(toCouponBody(form({ duration: "COUPON_DURATION_REPEATING", periods: "0" }), now)).toEqual({ error: "periods" });
    expect(toCouponBody(form({ max: "0" }), now)).toEqual({ error: "max" });
    expect(toCouponBody(form({ expires: "2026-10-01" }), now)).toEqual({ error: "expires" });
  });

  it("shows what a coupon does to an example", () => {
    expect(couponEffect("percent", "10", "USD")).toEqual({ kind: "percent", bps: 1000, currency: "USD", before: "10000", after: "9000" });
    expect(couponEffect("percent", "12.5", "JPY")).toEqual({ kind: "percent", bps: 1250, currency: "JPY", before: "100", after: "88" });
    expect(couponEffect("amount", "4.99", "USD")).toEqual({ kind: "amount", currency: "USD", off: "499", before: "10000", after: "9501" });
    expect(couponEffect("amount", "500", "USD")).toMatchObject({ after: "0" });
    expect(couponEffect("bonus", "25", "USD")).toEqual({ kind: "bonus", bps: 2500, credits: 1000, bonus: 250 });
    expect(couponEffect("percent", "abc", "USD")).toBeNull();
    expect(couponEffect("bonus", "0", "USD")).toBeNull();
  });

  it("reads a bonus percentage up to 1000", () => {
    expect(parseBonus("1000")).toBe(100000);
    expect(parseBonus("1000.01")).toBeNull();
    expect(parseBonus("2,5")).toBe(250);
  });
});
