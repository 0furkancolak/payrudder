import { nextTick } from "vue";
import { describe, expect, it } from "vitest";
import {
  affiliateTone,
  balancesByAffiliate,
  bpsToPercent,
  commissionTone,
  dayEnd,
  dayStart,
  defaultProgramForm,
  formToProgram,
  funnelCommissions,
  funnelSteps,
  journalTotals,
  payoutTone,
  percentToBps,
  programToForm,
  sumBalances,
} from "./logic";
import { usePager } from "./paging";

describe("basis points and percentages", () => {
  it("shows basis points as the percentage a person edits", () => {
    expect(bpsToPercent(1000)).toBe("10");
    expect(bpsToPercent(825)).toBe("8.25");
    expect(bpsToPercent(1250)).toBe("12.5");
    expect(bpsToPercent(1)).toBe("0.01");
    expect(bpsToPercent(10000)).toBe("100");
  });
  it("reads a typed percentage into basis points on the text, with at most two decimals", () => {
    expect(percentToBps("10")).toBe(1000);
    expect(percentToBps("12,5")).toBe(1250);
    expect(percentToBps("8.25")).toBe(825);
    expect(percentToBps("0.07")).toBe(7);
    expect(percentToBps("100")).toBe(10000);
    expect(percentToBps("100.01")).toBeNull();
    expect(percentToBps("8.255")).toBeNull();
    expect(percentToBps("abc")).toBeNull();
    expect(percentToBps("-1")).toBeNull();
  });
  it("round-trips every basis point of a percentage up to 100", () => {
    for (let bps = 0; bps <= 10000; bps += 7) expect(percentToBps(bpsToPercent(bps))).toBe(bps);
  });
});

describe("the program form", () => {
  it("starts from the defaults before a program exists", () => {
    const f = defaultProgramForm("TRY");
    expect(f).toMatchObject({ status: "draft", kind: "percent_bps", value: "10", term: "lifetime", windowDays: "30", holdDays: "30", clickCap: "1000", currency: "TRY" });
    const r = formToProgram(f);
    expect("body" in r && r.body).toMatchObject({ status: "draft", commission_kind: "percent_bps", commission_value: "1000", commission_term: "lifetime", attribution_window_days: 30, hold_days: 30, daily_click_cap: 1000, min_payout_minor: "0", auto_approve: false });
    // A percentage sends no currency and a lifetime term no value.
    expect("body" in r && "currency" in r.body).toBe(false);
    expect("body" in r && "commission_term_value" in r.body).toBe(false);
  });

  it("sends a fixed amount in minor units of its currency", () => {
    const f = { ...defaultProgramForm("JPY"), kind: "fixed" as const, value: "500", minPayout: "5000" };
    const r = formToProgram(f);
    expect("body" in r && r.body).toMatchObject({ commission_kind: "fixed", commission_value: "500", currency: "JPY", min_payout_minor: "5000" });
    const usd = formToProgram({ ...defaultProgramForm("USD"), kind: "fixed", value: "5.5", minPayout: "25" });
    expect("body" in usd && usd.body).toMatchObject({ commission_value: "550", min_payout_minor: "2500", currency: "USD" });
  });

  it("loads a stored program back into the form", () => {
    const f = programToForm(
      { status: "active", commission_kind: "percent_bps", commission_value: "825", commission_term: "months", commission_term_value: 12, attribution_window_days: 60, hold_days: 14, min_payout_minor: "2500", referred_coupon_code: "WELCOME", daily_click_cap: 500, landing_url: "https://example.com", auto_approve: true },
      "USD",
    );
    expect(f).toMatchObject({ status: "active", kind: "percent_bps", value: "8.25", term: "months", termValue: "12", windowDays: "60", holdDays: "14", minPayout: "25.00", coupon: "WELCOME", clickCap: "500", landingUrl: "https://example.com", autoApprove: true, currency: "USD" });
    const fixed = programToForm({ commission_kind: "fixed", commission_value: "1250", currency: "EUR", attribution_window_days: 30, daily_click_cap: 1000 }, "USD");
    expect(fixed).toMatchObject({ kind: "fixed", value: "12.50", currency: "EUR" });
    // Saving what was loaded changes nothing.
    const again = formToProgram(f);
    expect("body" in again && again.body).toMatchObject({ commission_value: "825", commission_term_value: 12, min_payout_minor: "2500" });
  });

  it("names the first thing wrong", () => {
    const base = defaultProgramForm("USD");
    const problem = (patch: Partial<typeof base>) => {
      const r = formToProgram({ ...base, ...patch });
      return "problem" in r ? r.problem : null;
    };
    expect(problem({ value: "0" })).toBe("value");
    expect(problem({ value: "101" })).toBe("value");
    expect(problem({ kind: "fixed", value: "1.234" })).toBe("value");
    expect(problem({ term: "months", termValue: "" })).toBe("term");
    expect(problem({ term: "first_invoices", termValue: "0" })).toBe("term");
    expect(problem({ windowDays: "0" })).toBe("window");
    expect(problem({ windowDays: "366" })).toBe("window");
    expect(problem({ holdDays: "181" })).toBe("hold");
    expect(problem({ holdDays: "0" })).toBeNull();
    expect(problem({ minPayout: "x" })).toBe("minPayout");
    expect(problem({ clickCap: "100001" })).toBe("clickCap");
    expect(problem({ landingUrl: "example.com" })).toBe("landing");
    expect(problem({ landingUrl: "https://example.com/welcome" })).toBeNull();
  });
});

describe("balances and the funnel", () => {
  it("adds partners up per currency and never across currencies", () => {
    const sums = sumBalances([
      { affiliate_id: "a", currency: "USD", pending_minor: "100", approved_minor: "200", paid_minor: "300", reversed_minor: "10" },
      { affiliate_id: "b", currency: "USD", pending_minor: "1", approved_minor: "2", paid_minor: "3", reversed_minor: "4" },
      { affiliate_id: "a", currency: "TRY", pending_minor: "9007199254740993", approved_minor: "1", paid_minor: "0", reversed_minor: "0" },
    ]);
    expect(sums).toEqual([
      { currency: "TRY", pending: "9007199254740993", approved: "1", paid: "0", reversed: "0" },
      { currency: "USD", pending: "101", approved: "202", paid: "303", reversed: "14" },
    ]);
    expect(sumBalances(undefined)).toEqual([]);
  });

  it("groups the rows by partner", () => {
    const by = balancesByAffiliate([
      { affiliate_id: "a", currency: "USD" },
      { affiliate_id: "a", currency: "EUR" },
      { affiliate_id: "b", currency: "USD" },
    ]);
    expect(by.get("a")?.map((b) => b.currency)).toEqual(["USD", "EUR"]);
    expect(by.get("b")?.length).toBe(1);
  });

  it("shapes the funnel steps with the conversion from the step before", () => {
    const steps = funnelSteps({ clicks: "200", referrals: "50", paying_customers: "10", commissions: [] });
    expect(steps).toEqual([
      { key: "clicks", count: 200, rate: null },
      { key: "referrals", count: 50, rate: 25 },
      { key: "paying", count: 10, rate: 20 },
    ]);
    // No clicks: no rate for the referrals rather than a division by zero; no paying customer of 3 referrals is 0 %.
    expect(funnelSteps({ clicks: "0", referrals: "3" }).map((s) => s.rate)).toEqual([null, null, 0]);
    expect(funnelSteps(undefined).map((s) => s.count)).toEqual([0, 0, 0]);
  });

  it("keeps the funnel's commission per currency with what remains after reversals", () => {
    const rows = funnelCommissions({ commissions: [{ currency: "USD", earned_minor: "1000", reversed_minor: "250" }, { currency: "EUR", earned_minor: "500", reversed_minor: "0" }] });
    expect(rows).toEqual([
      { currency: "EUR", earned: "500", reversed: "0", net: "500" },
      { currency: "USD", earned: "1000", reversed: "250", net: "750" },
    ]);
  });

  it("calls a journal balanced only when debits equal credits in that currency", () => {
    const t = journalTotals([
      { currency: "USD", debit_minor: "5000", credit_minor: "5000" },
      { currency: "EUR", debit_minor: "5000", credit_minor: "4999" },
    ]);
    expect(t).toEqual([
      { currency: "EUR", debit: "5000", credit: "4999", balanced: false },
      { currency: "USD", debit: "5000", credit: "5000", balanced: true },
    ]);
  });
});

describe("statuses", () => {
  it("gives every status a tone, with the word always shown next to it", () => {
    expect(affiliateTone("active")).toBe("success");
    expect(affiliateTone("pending")).toBe("warning");
    expect(affiliateTone("suspended")).toBe("danger");
    expect(commissionTone("pending")).toBe("warning");
    expect(commissionTone("approved")).toBe("info");
    expect(commissionTone("paid")).toBe("success");
    expect(commissionTone("reversed")).toBe("danger");
    expect(payoutTone("open")).toBe("info");
    expect(payoutTone("paid")).toBe("success");
    expect(payoutTone("cancelled")).toBe("neutral");
    expect(affiliateTone(undefined)).toBe("neutral");
  });
});

describe("days and paging", () => {
  it("turns a picked day into the instants the API takes, the last day included", () => {
    expect(dayStart("2026-10-21")).toBe("2026-10-21T00:00:00Z");
    expect(dayEnd("2026-10-21")).toBe("2026-10-22T00:00:00Z");
    expect(dayEnd("2026-12-31")).toBe("2027-01-01T00:00:00Z");
    expect(dayStart("")).toBeUndefined();
    expect(dayEnd("21/10/2026")).toBeUndefined();
  });

  it("pages forward by token and back again", () => {
    const p = usePager();
    expect(p.token.value).toBe("");
    expect(p.hasPrev.value).toBe(false);
    p.next("t2");
    p.next("t3");
    expect(p.token.value).toBe("t3");
    expect(p.hasPrev.value).toBe(true);
    p.prev();
    expect(p.token.value).toBe("t2");
    p.prev();
    expect(p.token.value).toBe("");
    expect(p.hasPrev.value).toBe(false);
    p.next("x");
    p.reset();
    expect(p.token.value).toBe("");
  });

  it("counts the page, and a new page size starts again from the first", async () => {
    const p = usePager();
    expect(p.page.value).toBe(1);
    p.next("t2");
    p.next("t3");
    expect(p.page.value).toBe(3);
    p.size.value = 50;
    await nextTick();
    expect(p.page.value).toBe(1);
    expect(p.token.value).toBe("");
  });
});
