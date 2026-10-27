import { describe, expect, it } from "vitest";
import { FIXTURES } from "@/shared/lib/receipt/fixtures";
import { parseReceipt } from "@/shared/lib/receipt/parse";
import type { ExpenseRule } from "./api";
import { canAutoApprove, matchRule, taxInside, withinTolerance } from "./rules";

const aws: ExpenseRule = { id: "1", vendor_match: "acme cloud", category: "infrastructure", currency: "USD", tax_bps: 825, expected_amount_minor: "12877", tolerance_bps: 500, auto_approve: true, app_id: "" };
const generic: ExpenseRule = { id: "2", vendor_match: "acme", category: "other", currency: "USD", tax_bps: 0, expected_amount_minor: "0", tolerance_bps: 0, auto_approve: false, app_id: "" };

describe("matchRule", () => {
  it("matches the vendor by contained text, case-insensitively, the longest rule first", () => {
    expect(matchRule([generic, aws], "ACME CLOUD, Inc.")?.id).toBe("1");
    expect(matchRule([generic, aws], "Acme Hardware")?.id).toBe("2");
    expect(matchRule([generic, aws], "Other Co")).toBeUndefined();
    expect(matchRule([aws], "")).toBeUndefined();
  });
  it("keeps a rule of one app to that app's inbox", () => {
    const scoped = { ...aws, app_id: "app-1" };
    expect(matchRule([scoped], "Acme Cloud", "app-1")).toBeDefined();
    expect(matchRule([scoped], "Acme Cloud", "app-2")).toBeUndefined();
    expect(matchRule([scoped], "Acme Cloud")).toBeUndefined();
  });
});

describe("withinTolerance", () => {
  it("compares in integer minor units, in the rule's currency only", () => {
    expect(withinTolerance(aws, 12877, "USD")).toBe(true);
    expect(withinTolerance(aws, 13520, "USD")).toBe(true); // +5.0% exactly
    expect(withinTolerance(aws, 13521, "USD")).toBe(false);
    expect(withinTolerance(aws, 12234, "USD")).toBe(true); // -5.0% exactly
    expect(withinTolerance(aws, 12233, "USD")).toBe(false);
    expect(withinTolerance(aws, 12877, "EUR")).toBe(false);
    expect(withinTolerance({ ...aws, expected_amount_minor: "0" }, 100, "USD")).toBe(false);
  });
});

describe("taxInside", () => {
  it("is the tax part of an amount that includes it", () => {
    expect(taxInside(12000, 2000)).toBe(2000);
    expect(taxInside(10000, 0)).toBe(0);
    expect(taxInside(1056, 825)).toBe(80);
  });
});

describe("canAutoApprove", () => {
  it("approves a document that matches its rule and is read with confidence", () => {
    expect(canAutoApprove(aws, parseReceipt(FIXTURES.usSaas))).toBe(true);
  });
  it("leaves it for review when the amount moved beyond the tolerance", () => {
    const parsed = parseReceipt(FIXTURES.usSaas.replaceAll("128.77", "428.77"));
    expect(canAutoApprove(aws, parsed)).toBe(false);
  });
  it("leaves it for review when the rule does not approve, or the amount or date was not read", () => {
    expect(canAutoApprove({ ...aws, auto_approve: false }, parseReceipt(FIXTURES.usSaas))).toBe(false);
    expect(canAutoApprove(undefined, parseReceipt(FIXTURES.usSaas))).toBe(false);
    expect(canAutoApprove(aws, parseReceipt("Acme Cloud, Inc.\nsome words"))).toBe(false);
  });
});
