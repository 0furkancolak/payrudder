import { describe, expect, it } from "vitest";
import { bpPercent, chosenCurrency, delta, geometry, hasActivity, nearestIndex, niceMax, parseQuery, share, toParams, toSearch, type DashCurrency } from "./lib";

describe("the query in the address", () => {
  it("defaults to 30 days, compared, first currency", () => {
    expect(parseQuery({})).toEqual({ period: "30d", compare: true });
    expect(toSearch(parseQuery({}))).toEqual({});
  });
  it("round-trips a chosen view", () => {
    const q = parseQuery({ period: "custom", from: "2026-09-01", to: "2026-09-30", compare: "off", cur: "EUR" });
    expect(q).toEqual({ period: "custom", from: "2026-09-01", to: "2026-09-30", compare: false, cur: "EUR" });
    expect(parseQuery(toSearch(q))).toEqual(q);
    expect(toParams(q)).toEqual({ period: "custom", from: "2026-09-01", to: "2026-09-30", compare: "0" });
    expect(toSearch(q).compare).toBe("off");
    expect(parseQuery({ compare: "0" }).compare).toBe(false);
  });
  it("ignores what it does not understand", () => {
    expect(parseQuery({ period: "weekly", cur: "euro" })).toEqual({ period: "30d", compare: true });
    expect(parseQuery({ period: "custom", from: "yesterday", to: "today" }).period).toBe("30d");
    expect(toParams(parseQuery({ period: "ytd" }))).toEqual({ period: "ytd", compare: "1" });
  });
});

describe("delta", () => {
  it("is the change from the earlier figure, to one decimal", () => {
    expect(delta(150, 100)).toEqual({ percent: 50, direction: "up" });
    expect(delta(75, 100)).toEqual({ percent: -25, direction: "down" });
    expect(delta(1, 3)).toEqual({ percent: -66.7, direction: "down" });
    expect(delta(100, 100)).toEqual({ percent: 0, direction: "flat" });
  });
  it("has no percentage from nothing: it is new", () => {
    expect(delta(500, 0)).toEqual({ percent: null, direction: "new" });
    expect(delta(0, 0)).toEqual({ percent: 0, direction: "flat" });
  });
  it("has nothing to say without an earlier figure", () => {
    expect(delta(5, undefined)).toEqual({ percent: null, direction: "none" });
    expect(delta(5, null)).toEqual({ percent: null, direction: "none" });
    expect(delta(undefined, 5).direction).toBe("none");
  });
  it("reads a fall from a negative figure as a fall", () => {
    expect(delta(-50, -100).direction).toBe("up");
    expect(delta(-150, -100).direction).toBe("down");
  });
  it("turns basis points into percent", () => {
    expect(bpPercent(1250)).toBe(12.5);
    expect(bpPercent(0)).toBe(0);
    expect(bpPercent(null)).toBeNull();
  });
});

describe("the currency tab", () => {
  const blocks = [{ currency: "TRY" }, { currency: "EUR" }] as DashCurrency[];
  it("is the one asked for, else the first", () => {
    expect(chosenCurrency(blocks, "EUR")?.currency).toBe("EUR");
    expect(chosenCurrency(blocks, "JPY")?.currency).toBe("TRY");
    expect(chosenCurrency(blocks, undefined)?.currency).toBe("TRY");
    expect(chosenCurrency([], "EUR")).toBeUndefined();
  });
  it("knows an empty block", () => {
    expect(hasActivity({ currency: "TRY", revenue: {} } as DashCurrency)).toBe(false);
    expect(hasActivity({ currency: "TRY", revenue: { gross_minor: 5 } } as DashCurrency)).toBe(true);
    expect(hasActivity({ currency: "TRY", revenue: {}, subscriptions: { active: 1 } } as DashCurrency)).toBe(true);
    expect(hasActivity({ currency: "TRY", revenue: {}, open_count: 2 } as DashCurrency)).toBe(true);
  });
});

describe("chart geometry", () => {
  it("rounds the top of the axis up to a round number", () => {
    expect(niceMax(0)).toBe(1);
    expect(niceMax(7)).toBe(10);
    expect(niceMax(120)).toBe(200);
    expect(niceMax(210)).toBe(250);
    expect(niceMax(260)).toBe(500);
    expect(niceMax(1000)).toBe(1000);
    expect(niceMax(0.3)).toBeCloseTo(0.5);
  });
  it("puts the largest value at the top and zero at the bottom", () => {
    const g = geometry([{ date: "a", value: 0 }, { date: "b", value: 100 }, { date: "c", value: 50 }], 300, 100);
    expect(g.max).toBe(100);
    expect(g.y(0)).toBeCloseTo(96);
    expect(g.y(100)).toBeCloseTo(8);
    expect(g.x(0)).toBeCloseTo(4);
    expect(g.x(2)).toBeCloseTo(296);
    expect(g.line.startsWith("M4 96")).toBe(true);
    expect(g.area.endsWith("Z")).toBe(true);
    expect(g.prevLine).toBe("");
  });
  it("scales both series by the same axis, so a comparison is honest", () => {
    const g = geometry([{ date: "a", value: 10, prev: 400 }, { date: "b", value: 20, prev: 300 }], 100, 100);
    expect(g.max).toBe(500);
    expect(g.prevLine).not.toBe("");
    expect(g.y(10)).toBeGreaterThan(g.y(400));
  });
  it("draws a single point and an empty series without breaking", () => {
    expect(geometry([{ date: "a", value: 5 }], 100, 50).line).toMatch(/^M4 .* L96 /);
    expect(geometry([], 100, 50).line).toBe("");
    expect(geometry([{ date: "a", value: 0 }, { date: "b", value: 0 }], 100, 50).max).toBe(1);
  });
  it("finds the point nearest to the pointer", () => {
    const g = geometry([{ date: "a", value: 1 }, { date: "b", value: 2 }, { date: "c", value: 3 }], 204, 50);
    expect(nearestIndex(g, 3, 0)).toBe(0);
    expect(nearestIndex(g, 3, 110)).toBe(1);
    expect(nearestIndex(g, 3, 400)).toBe(2);
    expect(nearestIndex(g, 1, 50)).toBe(0);
  });
});

describe("list bars", () => {
  it("sizes against the largest and leaves a sliver for a little", () => {
    expect(share(50, 100)).toBe(50);
    expect(share(100, 100)).toBe(100);
    expect(share(1, 1000)).toBe(2);
    expect(share(0, 100)).toBe(0);
    expect(share(5, 0)).toBe(0);
  });
});
