import { describe, expect, it } from "vitest";
import { kpiFigures, mergedCurrencies, mergedSeries, nextTabIndex, ratioBp, sumBlocks, type DashCurrency, type Dashboard } from "./lib";

// The generated types require every figure; a test names the ones it cares about and the rest are zero.
const rev = (o: Partial<DashCurrency["revenue"]> = {}): DashCurrency["revenue"] => ({ gross_minor: 0, tax_minor: 0, refunds_minor: 0, net_minor: 0, expenses_minor: 0, profit_minor: 0, paid_invoices: 0, ...o });
const subs = (o: Partial<DashCurrency["subscriptions"]> = {}): DashCurrency["subscriptions"] => ({
  month: "2026-10",
  active: 0,
  trialing: 0,
  paused: 0,
  past_due: 0,
  mrr_minor: 0,
  arr_minor: 0,
  new: 0,
  new_mrr_minor: 0,
  churned: 0,
  churned_mrr_minor: 0,
  churn_rate_bp: null,
  ...o,
});
const pay = (o: Partial<DashCurrency["payments"]> = {}): DashCurrency["payments"] => ({ paid: 0, failed: 0, processing: 0, refunds: 0, paid_minor: 0, failed_minor: 0, refunded_minor: 0, failed_rate_bp: null, ...o });

const base: DashCurrency = {
  currency: "USD",
  revenue: rev({ net_minor: 2000, gross_minor: 2200, refunds_minor: 200 }),
  prev_revenue: rev({ net_minor: 1000, gross_minor: 1000, refunds_minor: 0 }),
  subscriptions: subs({ mrr_minor: 500, churn_rate_bp: 250 }),
  prev_subscriptions: subs({ mrr_minor: 500, churn_rate_bp: 500 }),
  payments: pay({ failed_rate_bp: 1000 }),
  prev_payments: pay({ failed_rate_bp: 1000 }),
  arpu_minor: 40,
  prev_arpu_minor: 20,
  series: [],
  top_customers: [],
  top_packages: [],
  aging: [],
  open_count: 0,
  open_minor: 0,
  renewals: [],
};
// A spread of a Partial may leave a key undefined as far as the compiler knows; a test never passes undefined.
const block = (over: Partial<DashCurrency> = {}): DashCurrency => ({ ...base, ...over }) as DashCurrency;

describe("kpiFigures", () => {
  it("compares every figure with the period before", () => {
    const k = kpiFigures(block(), { new: 12, prev_new: 10, total: 30 }, true);
    expect(k.dNet).toEqual({ percent: 100, direction: "up" });
    expect(k.dMrr).toEqual({ percent: 0, direction: "flat" });
    expect(k.dArpu).toEqual({ percent: 100, direction: "up" });
    expect(k.dCustomers).toEqual({ percent: 20, direction: "up" });
    expect(k.churn).toBe(2.5);
    expect(k.prevChurn).toBe(5);
    expect(k.dChurn).toEqual({ percent: -50, direction: "down" });
    expect(k.dRate?.direction).toBe("flat");
    expect(k.dRefunds).toEqual({ percent: null, direction: "new" });
  });

  it("shows no change at all while the comparison is off", () => {
    const k = kpiFigures(block(), { new: 12, prev_new: 10, total: 30 }, false);
    expect([k.dNet, k.dMrr, k.dArpu, k.dCustomers, k.dChurn, k.dRate, k.dRefunds]).toEqual([null, null, null, null, null, null, null]);
    expect(k.churn).toBe(2.5);
  });

  it("has no ARPU change when the earlier figure is missing", () => {
    expect(kpiFigures(block({ prev_arpu_minor: null }), undefined, true).dArpu).toBeNull();
  });
});

describe("tab keys", () => {
  it("moves with the arrows, wraps around, and jumps with Home and End", () => {
    expect(nextTabIndex("ArrowRight", 2, 3)).toBe(0);
    expect(nextTabIndex("ArrowDown", 0, 3)).toBe(1);
    expect(nextTabIndex("ArrowLeft", 0, 3)).toBe(2);
    expect(nextTabIndex("ArrowUp", 2, 3)).toBe(1);
    expect(nextTabIndex("Home", 2, 3)).toBe(0);
    expect(nextTabIndex("End", 0, 3)).toBe(2);
    expect(nextTabIndex("a", 0, 3)).toBe(-1);
  });
});

describe("putting apps together", () => {
  it("rates a share in basis points and has none without attempts", () => {
    expect(ratioBp(1, 4)).toBe(2500);
    expect(ratioBp(0, 0)).toBeNull();
  });

  it("orders the currencies of several dashboards by net revenue, then by code", () => {
    const a = { currencies: [block({ currency: "EUR", revenue: rev({ net_minor: 10 }) }), block({ currency: "USD", revenue: rev({ net_minor: 50 }) })] } as Dashboard;
    const b = { currencies: [block({ currency: "EUR", revenue: rev({ net_minor: 100 }) }), block({ currency: "TRY", revenue: rev({ net_minor: 0 }) }), block({ currency: "GBP", revenue: rev({ net_minor: 0 }) })] } as Dashboard;
    expect(mergedCurrencies([a, b, undefined])).toEqual(["EUR", "USD", "GBP", "TRY"]);
  });

  it("adds series point by point and keeps the earlier period only where it exists", () => {
    const x = block({ series: [{ date: "2026-10-01", net_minor: 10, prev_net_minor: 4, prev_date: "2026-09-01" }, { date: "2026-10-02", net_minor: 20 }] });
    const y = block({ series: [{ date: "2026-10-01", net_minor: 1, prev_net_minor: 1, prev_date: "2026-09-01" }] });
    expect(mergedSeries([x, y, undefined])).toEqual([
      { date: "2026-10-01", value: 11, prev: 5, prevDate: "2026-09-01" },
      { date: "2026-10-02", value: 20 },
    ]);
    expect(mergedSeries([])).toEqual([]);
  });

  it("adds one figure of every block, counting a missing block as zero", () => {
    expect(sumBlocks([block(), undefined, block()], (b) => b.revenue?.net_minor)).toBe(4000);
  });
});
