import { describe, expect, it } from "vitest";
import { bounds, bucketStarts, chartPoints, compareDecimals, fillBuckets, formatQuantity, granularityFor, limitState, mergeCustomers, maxDecimal, percentUsed, periodOf, totalOf, previousRange, rangeDays, sumDecimals, validRange, worstState } from "./lib";

describe("ranges", () => {
  it("counts the days of a range, both ends included", () => {
    expect(rangeDays({ from: "2026-10-01", to: "2026-10-01" })).toBe(1);
    expect(rangeDays({ from: "2026-10-01", to: "2026-10-30" })).toBe(30);
    expect(rangeDays({ from: "2026-10-05", to: "2026-10-01" })).toBe(0);
    expect(rangeDays({ from: "", to: "" })).toBe(0);
  });

  it("accepts a range of one day to a year and nothing else", () => {
    expect(validRange({ from: "2026-01-01", to: "2026-12-31" })).toBe(true);
    expect(validRange({ from: "2025-01-01", to: "2026-12-31" })).toBe(false);
    expect(validRange({ from: "2026-02-01", to: "" })).toBe(false);
  });

  it("compares with the range of the same length right before", () => {
    expect(previousRange({ from: "2026-10-11", to: "2026-10-20" })).toEqual({ from: "2026-10-01", to: "2026-10-10" });
    expect(previousRange({ from: "2026-03-01", to: "2026-03-01" })).toEqual({ from: "2026-02-28", to: "2026-02-28" });
    expect(previousRange({ from: "2026-01-01", to: "2026-01-31" })).toEqual({ from: "2025-12-01", to: "2025-12-31" });
  });

  it("asks for instants from the start of the first day to the start of the day after the last", () => {
    expect(bounds({ from: "2026-10-01", to: "2026-10-31" })).toEqual({ from: "2026-10-01T00:00:00Z", to: "2026-11-01T00:00:00Z" });
  });

  it("names the billing period of a day, and draws one or two days by the hour", () => {
    expect(periodOf("2026-10-31")).toBe("2026-10");
    expect(granularityFor({ from: "2026-10-01", to: "2026-10-02" })).toBe("hour");
    expect(granularityFor({ from: "2026-10-01", to: "2026-10-03" })).toBe("day");
  });
});

describe("buckets", () => {
  const range = { from: "2026-10-01", to: "2026-10-03" };

  it("has a start for every day or hour of the range", () => {
    expect(bucketStarts(range, "day")).toEqual(["2026-10-01T00:00:00Z", "2026-10-02T00:00:00Z", "2026-10-03T00:00:00Z"]);
    expect(bucketStarts({ from: "2026-10-01", to: "2026-10-01" }, "hour")).toHaveLength(24);
  });

  it("puts zero where the server left a day out, so two periods line up", () => {
    const b = fillBuckets([{ start: "2026-10-02T00:00:00Z", quantity: "5.500", event_count: "3" }], range, "day");
    expect(b.map((x) => x.quantity)).toEqual(["0", "5.500", "0"]);
    expect(b.map((x) => x.events)).toEqual([0, 3, 0]);
  });

  it("lays the previous period under the current one point by point", () => {
    const cur = fillBuckets([{ start: "2026-10-03T00:00:00Z", quantity: "4" }], range, "day");
    const prev = fillBuckets([{ start: "2026-09-28T00:00:00Z", quantity: "2" }], previousRange(range), "day");
    const points = chartPoints(cur, prev);
    expect(points.map((p) => [p.value, p.prev])).toEqual([[0, 2], [0, 0], [4, 0]]);
    expect(points[0]?.prevDate).toBe("2026-09-28T00:00:00Z");
    expect(chartPoints(cur)[0]).not.toHaveProperty("prev");
  });
});

describe("decimals", () => {
  it("adds exactly, whatever the scales", () => {
    expect(sumDecimals(["0.1", "0.2"])).toBe("0.3");
    expect(sumDecimals(["1500.250", "0.5", "2"])).toBe("1502.750");
    expect(sumDecimals([])).toBe("0");
    expect(sumDecimals(["9007199254740993", "1"])).toBe("9007199254740994");
    expect(sumDecimals(["x", "2"])).toBe("2");
  });

  it("takes the highest bucket for a meter that keeps the highest value, and the sum for the others", () => {
    expect(maxDecimal(["1.5", "9", "3.250"])).toBe("9");
    expect(maxDecimal([])).toBe("0");
    expect(totalOf("AGGREGATION_MAX", ["4", "7.5", "2"])).toBe("7.5");
    expect(totalOf("AGGREGATION_SUM", ["4", "7.5", "2"])).toBe("13.5");
    expect(totalOf("AGGREGATION_COUNT", ["4", "1"])).toBe("5");
  });

  it("gives no total for a meter that counts distinct values or takes the latest value: raw event buckets cannot make one", () => {
    expect(totalOf("AGGREGATION_UNIQUE", ["4", "1"])).toBe("");
    expect(totalOf("AGGREGATION_LAST", ["4", "1"])).toBe("");
    expect(formatQuantity(totalOf("AGGREGATION_UNIQUE", ["4"]), "en")).toBe("—");
  });

  it("compares like a comparator", () => {
    expect(compareDecimals("10.5", "10.50")).toBe(0);
    expect(compareDecimals("2", "10")).toBe(-1);
    expect(compareDecimals("10.01", "10")).toBe(1);
  });

  it("measures the use of a limit to one decimal", () => {
    expect(percentUsed("50", "200")).toBe(25);
    expect(percentUsed("10.500", "30")).toBe(35);
    expect(percentUsed("1", "3")).toBe(33.3);
    expect(percentUsed("5", "0")).toBe(100);
    expect(percentUsed("0", "0")).toBe(0);
    expect(percentUsed("5", null)).toBeNull();
  });

  it("puts a customer under, near, at or without a limit", () => {
    expect(limitState({ used: "10", limit: "100", allowed: true })).toBe("ok");
    expect(limitState({ used: "80", limit: "100", allowed: true })).toBe("near");
    expect(limitState({ used: "100", limit: "100", allowed: false })).toBe("over");
    expect(limitState({ used: "150", limit: "100" })).toBe("over");
    expect(limitState({ used: "5", limit: null, unlimited: true, allowed: true })).toBe("unlimited");
    expect(worstState(["ok", "near", "unlimited"])).toBe("near");
    expect(worstState(["ok", "over", "near"])).toBe("over");
    expect(worstState([])).toBe("unlimited");
  });

  it("writes a quantity without the zeros of its scale", () => {
    expect(formatQuantity("1500.250", "en")).toBe("1,500.25");
    expect(formatQuantity("42.000", "en")).toBe("42");
    expect(formatQuantity("1500", "de")).toBe("1.500");
    expect(formatQuantity(undefined, "en")).toBe("—");
  });
});

describe("mergeCustomers", () => {
  it("adds the ranked usage to the customers, busiest first, and keeps a ranked customer the list lacks", () => {
    const rows = mergeCustomers(
      [
        { external_id: "b", name: "Bea", email: "b@x" },
        { external_id: "a", name: "Ada", email: "a@x" },
        { external_id: "idle", name: "Idle" },
      ],
      { calls: [{ customer_external_id: "b", quantity: "10", event_count: "10" }, { customer_external_id: "ghost", quantity: "1", event_count: "1" }], gb: [{ customer_external_id: "b", quantity: "2.5", event_count: "4" }] },
    );
    expect(rows.map((r) => r.externalId)).toEqual(["b", "ghost", "a", "idle"]);
    expect(rows[0]?.usage).toEqual({ calls: { quantity: "10", events: 10 }, gb: { quantity: "2.5", events: 4 } });
    expect(rows[3]?.usage).toEqual({});
  });
});
