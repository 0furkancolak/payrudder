import { describe, expect, it } from "vitest";
import { draftBody, draftProblem, fixedText, parseFixed, parseRate, rateText, ruleId, validAccountKey } from "../feerules";

describe("parseRate", () => {
  it("reads a percentage with at most two decimals as hundredths of a percent, without a float", () => {
    expect(parseRate("2.9")).toBe(290);
    expect(parseRate("2,9 %")).toBe(290);
    expect(parseRate("0.25")).toBe(25);
    expect(parseRate("1.1")).toBe(110); // a float would give 110.00000000000001
    expect(parseRate("0")).toBe(0);
    expect(parseRate("100")).toBe(10000);
  });
  it("refuses what the server would", () => {
    for (const bad of ["", "abc", "-1", "100.01", "101", "2.999", "1e2", "%", "2..9"]) expect(parseRate(bad), bad).toBeNull();
  });
});

describe("rateText", () => {
  it("is the inverse of parseRate", () => {
    for (const bps of [0, 1, 25, 100, 275, 290, 1000, 10000]) expect(parseRate(rateText(bps)), String(bps)).toBe(bps);
    expect(rateText(290)).toBe("2.9");
    expect(rateText(275)).toBe("2.75");
    expect(rateText(100)).toBe("1");
    expect(rateText(5)).toBe("0.05");
    expect(rateText(undefined)).toBe("0");
  });
});

describe("the fixed part", () => {
  it("is typed in the currency's own units and sent in minor units", () => {
    expect(parseFixed("0.30", "USD")).toBe(30);
    expect(parseFixed("0,3", "USD")).toBe(30);
    expect(parseFixed("5", "JPY")).toBe(5); // a currency with no minor unit
    expect(parseFixed("", "USD")).toBe(0);
    expect(parseFixed("0.305", "USD")).toBeNull(); // more digits than the currency has
    expect(parseFixed("-1", "USD")).toBeNull();
  });
  it("is shown back as it was typed, and empty when there is none", () => {
    expect(fixedText(30, "USD")).toBe("0.30");
    expect(fixedText(5, "JPY")).toBe("5");
    expect(fixedText(0, "USD")).toBe("");
    expect(fixedText(undefined, "USD")).toBe("");
  });
});

describe("a draft", () => {
  const ok = { provider: "stripe~eu", currency: "usd", rate: "2.9", fixed: "0.30" };
  it("becomes the body the server takes, the currency in capitals", () => {
    expect(draftProblem(ok)).toBeNull();
    expect(draftBody(ok)).toEqual({ provider: "stripe~eu", currency: "USD", rate_bps: 290, fixed_minor: 30 });
  });
  it("names the first thing wrong with it", () => {
    expect(draftProblem({ ...ok, provider: "Stripe~" })).toBe("provider");
    expect(draftProblem({ ...ok, provider: "" })).toBe("provider");
    expect(draftProblem({ ...ok, currency: "US" })).toBe("currency");
    expect(draftProblem({ ...ok, rate: "101" })).toBe("rate");
    expect(draftProblem({ ...ok, fixed: "0.001" })).toBe("fixed");
  });
  it("knows an account key from a provider name that is not one", () => {
    expect(validAccountKey("paddle")).toBe(true);
    expect(validAccountKey("paddle~eu")).toBe(true);
    expect(validAccountKey("paddle~eu~x")).toBe(false);
    expect(validAccountKey("Paddle")).toBe(false);
  });
  it("is named by its environment, provider and currency", () => {
    expect(ruleId({ environment: "live", provider: "stripe", currency: "USD" })).toBe("live:stripe:USD");
  });
});
