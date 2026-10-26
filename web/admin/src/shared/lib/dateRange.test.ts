import { describe, expect, it } from "vitest";
import { inRange, rangeOf } from "./dateRange";

const now = new Date(2026, 9, 26, 15, 0); // 26 October 2026

describe("rangeOf", () => {
  it("counts the last seven days including today", () => expect(rangeOf("7d", now)).toEqual({ from: "2026-10-20", to: "2026-10-26" }));
  it("is the month so far", () => expect(rangeOf("month", now)).toEqual({ from: "2026-10-01", to: "2026-10-26" }));
  it("is the whole of last month", () => expect(rangeOf("lastMonth", now)).toEqual({ from: "2026-09-01", to: "2026-09-30" }));
  it("is the year so far", () => expect(rangeOf("year", now)).toEqual({ from: "2026-01-01", to: "2026-10-26" }));
});

describe("inRange", () => {
  it("lets everything through when open", () => expect(inRange("2020-01-01T00:00:00Z", { from: "", to: "" })).toBe(true));
  it("keeps the ends", () => {
    expect(inRange("2026-10-20T12:00:00", { from: "2026-10-20", to: "2026-10-26" })).toBe(true);
    expect(inRange("2026-10-27T12:00:00", { from: "2026-10-20", to: "2026-10-26" })).toBe(false);
  });
  it("drops a row without a date once a range is set", () => expect(inRange(undefined, { from: "2026-10-01", to: "" })).toBe(false));
});
