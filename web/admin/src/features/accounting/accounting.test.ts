import { describe, expect, it } from "vitest";
import { barHeights, donutSlices } from "./chart";
import { granularityOf, presetRange, validRange } from "./period";

describe("period presets", () => {
  const now = new Date(2026, 9, 21); // 21 October 2026

  it("this month runs from the first to the last day", () => {
    expect(presetRange("thisMonth", now)).toEqual({ from: "2026-10-01", to: "2026-10-31" });
  });

  it("last month knows the length of the month and the turn of the year", () => {
    expect(presetRange("lastMonth", now)).toEqual({ from: "2026-09-01", to: "2026-09-30" });
    expect(presetRange("lastMonth", new Date(2026, 0, 15))).toEqual({ from: "2025-12-01", to: "2025-12-31" });
    expect(presetRange("lastMonth", new Date(2024, 2, 5))).toEqual({ from: "2024-02-01", to: "2024-02-29" });
  });

  it("the last 30 days include today", () => {
    expect(presetRange("last30", now)).toEqual({ from: "2026-09-22", to: "2026-10-21" });
  });

  it("this year is the year to date", () => {
    expect(presetRange("thisYear", now)).toEqual({ from: "2026-01-01", to: "2026-10-21" });
  });

  it("custom keeps what was typed", () => {
    expect(presetRange("custom", now, { from: "2026-02-01", to: "2026-02-10" })).toEqual({ from: "2026-02-01", to: "2026-02-10" });
  });

  it("accepts only an ordered range of at most five years", () => {
    expect(validRange({ from: "2026-02-01", to: "2026-02-01" })).toBe(true);
    expect(validRange({ from: "2026-02-02", to: "2026-02-01" })).toBe(false);
    expect(validRange({ from: "", to: "2026-02-01" })).toBe(false);
    expect(validRange({ from: "2021-01-01", to: "2026-01-01" })).toBe(true);
    expect(validRange({ from: "2021-01-01", to: "2026-01-02" })).toBe(false);
  });

  it("reads a short period by day and a long one by month", () => {
    expect(granularityOf({ from: "2026-10-01", to: "2026-10-31" })).toBe("day");
    expect(granularityOf({ from: "2026-01-01", to: "2026-10-21" })).toBe("month");
  });
});

describe("donut", () => {
  it("lays slices end to end from 12 o'clock", () => {
    const s = donutSlices([
      { category: "infrastructure", amount_minor: "4000" },
      { category: "software", amount_minor: "1000" },
    ]);
    expect(s.map((x) => x.category)).toEqual(["infrastructure", "software"]);
    expect(s[0]!.percent).toBeCloseTo(80);
    expect(s[0]!.offset).toBe(25);
    expect(s[1]!.offset).toBeCloseTo(25 - 80);
    expect(s.reduce((a, x) => a + x.percent, 0)).toBeCloseTo(100);
  });

  it("has no slices without money, and ignores empty categories", () => {
    expect(donutSlices([])).toEqual([]);
    expect(donutSlices([{ category: "other", amount_minor: "0" }])).toEqual([]);
    expect(donutSlices([{ category: "other", amount_minor: "0" }, { category: "tax", amount_minor: "5" }])).toHaveLength(1);
  });
});

describe("bars", () => {
  it("scales to the tallest value and draws no income bar for a loss", () => {
    const bars = barHeights([
      { bucket: "2026-09-01", net_income_minor: "10000", expenses_minor: "2500" },
      { bucket: "2026-10-01", net_income_minor: "-300", expenses_minor: "5000" },
    ]);
    expect(bars[0]).toEqual({ bucket: "2026-09-01", income: 1, expense: 0.25 });
    expect(bars[1]).toEqual({ bucket: "2026-10-01", income: 0, expense: 0.5 });
  });

  it("is flat when everything is zero", () => {
    expect(barHeights([{ bucket: "2026-10-01", net_income_minor: "0", expenses_minor: "0" }])).toEqual([{ bucket: "2026-10-01", income: 0, expense: 0 }]);
  });
});
