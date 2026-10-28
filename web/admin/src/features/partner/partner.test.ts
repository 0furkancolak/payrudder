import { describe, expect, it } from "vitest";
import { buildNav } from "@/app/layout/nav";
import { earnedByCurrency, environmentTone, hasPartnerRole, isReversal, ownMemberships, programTerms, safeNext, sortAffiliations, sumBalances, affiliateTone, type Affiliation, type MyProgram } from "./logic";
import { partnerRoutes } from "./routes";
import { createRoute, type AnyRoute } from "@tanstack/vue-router";

const money = (minor: string, currency: string) => `${currency} ${minor}`;

describe("memberships of the role affiliate", () => {
  const ms = [
    { account_id: "seller", role: "affiliate" },
    { account_id: "mine", role: "owner" },
    { account_id: "other", role: "viewer" },
  ];
  it("are not accounts of the person", () => {
    expect(ownMemberships(ms).map((m) => m.account_id)).toEqual(["mine", "other"]);
    expect(ownMemberships([{ account_id: "seller", role: "affiliate" }])).toEqual([]);
    expect(ownMemberships(undefined)).toEqual([]);
  });
  it("say whether the person is a partner", () => {
    expect(hasPartnerRole(ms)).toBe(true);
    expect(hasPartnerRole(ownMemberships(ms))).toBe(false);
    expect(hasPartnerRole(undefined)).toBe(false);
  });
  it("give the organisation level a Partner portal entry only for a partner", () => {
    const keys = (partner: boolean) => buildNav({ level: "org", partner }).flatMap((s) => s.items.map((i) => i.key));
    expect(keys(false)).toEqual(["members", "orgIntegrations", "support", "settings"]);
    expect(keys(true)).toEqual(["members", "partner", "orgIntegrations", "support", "settings"]);
    const item = buildNav({ level: "org", partner: true }).flatMap((s) => s.items).find((i) => i.key === "partner");
    expect(item?.to).toBe("/app/partner");
    expect(item?.owns("/app/partner/abc")).toBe(true);
    expect(item?.owns("/app/partnership")).toBe(false);
  });
});

describe("the portal routes", () => {
  it("are session pages under /app: the home (an affiliation opens in its sheet) and the apply page", () => {
    const parent = createRoute({ getParentRoute: () => ({}) as AnyRoute, id: "person" });
    const paths = partnerRoutes(parent).map((r) => (r.options as { path?: string }).path);
    expect(paths).toEqual(["/app/partner", "/app/partner/apply"]);
  });
});

describe("the sign-in return path", () => {
  it("accepts a path of this console, with its query", () => {
    expect(safeNext("/app/partner/apply?tenant=abc")).toBe("/app/partner/apply?tenant=abc");
    expect(safeNext("/app")).toBe("/app");
  });
  it("refuses anything that could leave the console or loop", () => {
    for (const v of ["https://evil.example/x", "//evil.example", "/\\evil", "javascript:alert(1)", "app/partner", "", "/signin?next=/app", "/signup", "/reset?token=x", "/a\nb", undefined, 5, "/" + "a".repeat(600)]) {
      expect(safeNext(v), String(v)).toBe("");
    }
  });
});

describe("balances and earnings per currency", () => {
  it("keeps currencies apart and adds nothing across them", () => {
    const sums = sumBalances([
      { affiliate_id: "a1", currency: "USD", pending_minor: "0", approved_minor: "900", paid_minor: "0", reversed_minor: "0" },
      { affiliate_id: "a1", currency: "TRY", pending_minor: "400", approved_minor: "2500", paid_minor: "7000", reversed_minor: "300" },
    ]);
    expect(sums).toEqual([
      { currency: "TRY", pending: "400", approved: "2500", paid: "7000", reversed: "300" },
      { currency: "USD", pending: "0", approved: "900", paid: "0", reversed: "0" },
    ]);
  });
  it("shapes what was earned with what is left after taking back", () => {
    expect(earnedByCurrency([{ currency: "USD", earned_minor: "2500", reversed_minor: "0" }, { currency: "TRY", earned_minor: "10000", reversed_minor: "1500" }])).toEqual([
      { currency: "TRY", earned: "10000", reversed: "1500", net: "8500" },
      { currency: "USD", earned: "2500", reversed: "0", net: "2500" },
    ]);
    expect(earnedByCurrency(undefined)).toEqual([]);
  });
});

describe("the terms in words", () => {
  const base: MyProgram = { status: "active", commission_kind: "percent_bps", commission_value: "1250", commission_term: "months", commission_term_value: 12, attribution_window_days: 45, hold_days: 14, min_payout_minor: "2500", currency: "TRY" };
  it("reads a percentage from basis points", () => {
    expect(programTerms(base, money)).toEqual([
      { key: "percent", params: { pct: "12.5" } },
      { key: "months", params: { n: 12 } },
      { key: "window", params: { days: 45 } },
      { key: "hold", params: { days: 14 } },
      { key: "minPayout", params: { amount: "TRY 2500" } },
    ]);
  });
  it("reads a fixed amount, a number of invoices and a lifetime term, and a program with no minimum", () => {
    const fixed = programTerms({ ...base, commission_kind: "fixed", commission_value: "500", commission_term: "first_invoices", commission_term_value: 3, min_payout_minor: "0" }, money);
    expect(fixed.map((l) => l.key)).toEqual(["fixed", "firstInvoices", "window", "hold", "noMinimum"]);
    expect(fixed[0]?.params).toEqual({ amount: "TRY 500" });
    expect(programTerms({ ...base, commission_term: "lifetime", commission_term_value: 0, commission_value: "1000" }, money)[1]).toEqual({ key: "lifetime", params: {} });
    expect(programTerms(undefined, money)).toEqual([]);
  });
  it("leaves the minimum out when no currency can read it", () => {
    expect(programTerms({ ...base, currency: "" }, money).map((l) => l.key)).toContain("noMinimum");
  });
});

describe("badges and order", () => {
  it("marks the sandbox so it is not mistaken for live, and gives every status a tone", () => {
    expect(environmentTone("sandbox")).toBe("warning");
    expect(environmentTone("live")).toBe("info");
    expect(affiliateTone("pending")).toBe("warning");
    expect(affiliateTone("suspended")).toBe("danger");
    expect(affiliateTone("active")).toBe("success");
  });
  it("lists live affiliations first, newest first", () => {
    const affiliation = (o: Pick<Affiliation, "id" | "environment" | "create_time">): Affiliation => ({
      tenant_id: "t1",
      seller_name: "Seller",
      code: "CODE",
      link: "https://example.test/r/CODE",
      status: "active",
      name: "Partner",
      email: "partner@example.test",
      payout_method: "manual",
      payout_details: "",
      program: null,
      ...o,
    });
    const list = sortAffiliations([
      affiliation({ id: "s", environment: "sandbox", create_time: "2026-10-03T00:00:00Z" }),
      affiliation({ id: "old", environment: "live", create_time: "2026-10-01T00:00:00Z" }),
      affiliation({ id: "new", environment: "live", create_time: "2026-10-02T00:00:00Z" }),
    ]);
    expect(list.map((a) => a.id)).toEqual(["new", "old", "s"]);
  });
  it("tells a reversal row from a commission", () => {
    expect(isReversal({ kind: "reversal" })).toBe(true);
    expect(isReversal({ kind: "commission" })).toBe(false);
  });
});
