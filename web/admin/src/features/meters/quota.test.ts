import { describe, expect, it } from "vitest";
import en from "@/locales/en.json";
import { AGGREGATIONS, aggregationsFor, categories, isQuantity, isRowShaped, modeWord, newQuota, parseThresholds, quotaDraftOf, quotaTerms, scaleOf, WINDOWS, windowWord, type Meter } from "./quota";

describe("quotaTerms", () => {
  it("builds the API's terms, leaving out default thresholds", () => {
    expect(quotaTerms({ limit: " 1000 ", window: "QUOTA_WINDOW_DAY", mode: "QUOTA_MODE_SOFT", thresholds: "50, 100" }, 0)).toEqual({
      terms: { limit: "1000", window: "QUOTA_WINDOW_DAY", mode: "QUOTA_MODE_SOFT", alert_thresholds_pct: [50, 100] },
    });
    expect(quotaTerms({ ...newQuota(), limit: "2.5" }, 1)).toEqual({ terms: { limit: "2.5", window: "QUOTA_WINDOW_MONTH", mode: "QUOTA_MODE_HARD" } });
  });
  it("names the wrong field", () => {
    expect(quotaTerms({ ...newQuota(), limit: "2.55" }, 1)).toEqual({ error: "limit" });
    expect(quotaTerms({ ...newQuota(), limit: "2", thresholds: "100, 50" }, 0)).toEqual({ error: "thresholds" });
  });
  it("round-trips through a draft", () => {
    expect(quotaDraftOf({ limit: "5", alert_thresholds_pct: [90], window: "QUOTA_WINDOW_UNSPECIFIED" })).toEqual({ limit: "5", window: "QUOTA_WINDOW_MONTH", mode: "QUOTA_MODE_HARD", thresholds: "90" });
  });
});

describe("parseThresholds", () => {
  it("reads percentages however they are separated", () => {
    expect(parseThresholds("50, 80, 100")).toEqual([50, 80, 100]);
    expect(parseThresholds("50 80% 120")).toEqual([50, 80, 120]);
    expect(parseThresholds("  ")).toEqual([]);
  });
  it("refuses what the API refuses", () => {
    for (const bad of ["80, 50", "0", "1001", "50, 50", "1.5", "a", "1,2,3,4,5,6,7,8,9,10,11"]) expect(parseThresholds(bad), bad).toBeNull();
  });
});

describe("isQuantity", () => {
  it("follows the meter's decimal places", () => {
    expect(isQuantity("1000", 0)).toBe(true);
    expect(isQuantity("2.50", 1)).toBe(true);
    expect(isQuantity("2.55", 1)).toBe(false);
    expect(isQuantity("-1", 3)).toBe(false);
    expect(isQuantity("", 3)).toBe(false);
  });
});

describe("windows", () => {
  it("offers every window shortest first, each with a label and a help text", () => {
    expect(WINDOWS.map(windowWord)).toEqual(["hour", "day", "week", "month", "year", "lifetime"]);
    for (const w of WINDOWS) {
      expect(en.billing.quotas.windows[windowWord(w) as keyof typeof en.billing.quotas.windows]).toBeTruthy();
      expect(en.billing.quotas.windowHelp[windowWord(w) as keyof typeof en.billing.quotas.windowHelp]).toBeTruthy();
    }
  });
});

describe("words", () => {
  it("maps enum values to the locale keys, unspecified to the default", () => {
    expect(windowWord("QUOTA_WINDOW_DAY")).toBe("day");
    expect(windowWord("QUOTA_WINDOW_YEAR")).toBe("year");
    expect(windowWord("QUOTA_WINDOW_LIFETIME")).toBe("lifetime");
    expect(windowWord("QUOTA_WINDOW_UNSPECIFIED")).toBe("month");
    expect(windowWord(undefined)).toBe("month");
    expect(modeWord("QUOTA_MODE_NOTIFY")).toBe("notify");
    expect(modeWord(undefined)).toBe("hard");
  });
});

describe("the aggregations of a meter", () => {
  it("know distinct values and the latest value, which stay with their code", () => {
    expect(AGGREGATIONS).toEqual(["AGGREGATION_SUM", "AGGREGATION_COUNT", "AGGREGATION_MAX", "AGGREGATION_UNIQUE", "AGGREGATION_LAST"]);
    expect(aggregationsFor(undefined)).toEqual(AGGREGATIONS);
    expect(aggregationsFor("AGGREGATION_UNIQUE")).toEqual(["AGGREGATION_UNIQUE"]);
    expect(aggregationsFor("AGGREGATION_COUNT")).toEqual(["AGGREGATION_SUM", "AGGREGATION_COUNT", "AGGREGATION_MAX"]);
    expect(isRowShaped("AGGREGATION_UNIQUE")).toBe(true);
    expect(isRowShaped("AGGREGATION_SUM")).toBe(false);
  });
});

describe("meters", () => {
  const meter = (code: string, extra: Partial<Meter> = {}): Meter => ({ code, aggregation: "AGGREGATION_SUM", unit_label: "u", ...extra });
  const meters = [meter("a", { category: "AI", quantity_scale: 3 }), meter("b", { category: " ai " }), meter("c", { category: "Compute" }), meter("d", { category: "AI" }), meter("e")];
  it("lists the categories in use once each", () => {
    expect(categories(meters)).toEqual(["ai", "AI", "Compute"].sort((x, y) => x.localeCompare(y)));
  });
  it("finds a meter's scale", () => {
    expect(scaleOf(meters, "a")).toBe(3);
    expect(scaleOf(meters, "zz")).toBe(0);
  });
});
