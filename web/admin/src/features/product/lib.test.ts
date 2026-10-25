import { describe, expect, it } from "vitest";
import type { AppOverview } from "./api";
import { countOf, revenueByCurrency, seriesOf } from "./lib";

const payments: NonNullable<AppOverview["payments"]> = {
  truncated: false,
  by_status: [
    { status: "paid", count: 5 },
    { status: "processing", count: 1 },
    { status: "failed", count: 2 },
    { status: "refunded", count: 0 },
  ],
  by_currency: [
    { currency: "TRY", paid_minor: 25000 },
    { currency: "USD", paid_minor: 4900 },
    { currency: "EUR", paid_minor: 4900 },
  ],
  series: [{ currency: "USD", points: [{ date: "2026-10-20", amount_minor: 100 }, { date: "2026-10-21", amount_minor: 4800 }] }],
  recent: [],
};

describe("overview helpers", () => {
  it("lists revenue per currency, the environment's own first, and never adds currencies together", () => {
    const rows = revenueByCurrency({ currency: "USD", payments });
    expect(rows.map((r) => r.currency)).toEqual(["USD", "TRY", "EUR"]);
    expect(rows[0]).toEqual({ currency: "USD", minor: 4900 });
    expect(rows).toHaveLength(3);
  });

  it("has no revenue rows without payments", () => {
    expect(revenueByCurrency({ currency: "USD", payments: null })).toEqual([]);
  });

  it("reads the series of a currency and falls back to nothing", () => {
    expect(seriesOf(payments, "USD")).toEqual([100, 4800]);
    expect(seriesOf(payments, "TRY")).toEqual([]);
    expect(seriesOf(null, "USD")).toEqual([]);
  });

  it("counts payments by status", () => {
    expect(countOf(payments, "failed")).toBe(2);
    expect(countOf(payments, "nope")).toBe(0);
  });
});
