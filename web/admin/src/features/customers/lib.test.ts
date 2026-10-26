import { describe, expect, it } from "vitest";
import { countryName, flagOf, headline, initialsOf, summarise } from "./lib";

describe("summarise", () => {
  const invoices = [
    { id: "i1", customer_external_id: "cus_1", status: "paid" as const, currency: "TRY", total_minor: "10000", payments: [] },
    { id: "i2", customer_external_id: "cus_1", status: "open" as const, currency: "TRY", total_minor: "5000", payments: [{ amount_minor: "2000", received_at: "2026-10-03T10:00:00Z" }] },
    { id: "i3", customer_external_id: "cus_1", status: "paid" as const, currency: "EUR", total_minor: "300" },
    { id: "i4", customer_external_id: "cus_2", status: "uncollectible" as const, currency: "USD", total_minor: "999" },
    { id: "i5", customer_external_id: "cus_2", status: "draft" as const, currency: "USD", total_minor: "1" },
  ];
  const payments = [
    { invoice_id: "i1", kind: "charge" as const, status: "paid" as const, amount_minor: "10000", currency: "TRY", occurred_at: "2026-10-01T10:00:00Z" },
    { invoice_id: "i1", kind: "charge" as const, status: "refunded" as const, amount_minor: "1500", currency: "TRY", occurred_at: "2026-10-05T10:00:00Z" },
    { invoice_id: "unknown", kind: "charge" as const, status: "paid" as const, amount_minor: "77", currency: "TRY", occurred_at: "2026-10-09T10:00:00Z" },
  ];
  const subscriptions = [
    { customer_external_id: "cus_1", state: "SUBSCRIPTION_STATE_ACTIVE" as const, plan_code: "pro" },
    { customer_external_id: "cus_1", state: "SUBSCRIPTION_STATE_CANCELLED" as const, plan_code: "old" },
    { customer_external_id: "cus_2", state: "SUBSCRIPTION_STATE_PAST_DUE" as const, plan_code: "pro" },
    { customer_external_id: "cus_3", state: "SUBSCRIPTION_STATE_TRIALING" as const, plan_code: "trial" },
  ];
  const by = summarise(invoices, payments, subscriptions);

  it("adds paid invoices per currency and takes refunds off", () => {
    expect(by.get("cus_1")?.spend).toEqual(new Map([["TRY", BigInt(8500)], ["EUR", BigInt(300)]]));
  });

  it("counts what open invoices still ask for, less what was recorded by hand", () => {
    expect(by.get("cus_1")?.open).toEqual(new Map([["TRY", BigInt(3000)]]));
    expect(by.get("cus_1")?.openInvoices).toBe(1);
  });

  it("takes the latest payment, charge or recorded by hand, and ignores payments of invoices it does not know", () => {
    expect(by.get("cus_1")?.lastPayment).toBe("2026-10-03T10:00:00Z");
    expect([...by.keys()].sort()).toEqual(["cus_1", "cus_2", "cus_3"]);
  });

  it("names the worst subscription state and the plans that bill now", () => {
    expect(by.get("cus_1")).toMatchObject({ subscriptionStatus: "active", activeSubscriptions: 1, subscriptions: 2, plans: ["pro"], delinquent: false });
    expect(by.get("cus_2")).toMatchObject({ subscriptionStatus: "pastDue", delinquent: true, uncollectibleInvoices: 1, invoices: 1 });
    expect(by.get("cus_3")).toMatchObject({ subscriptionStatus: "trialing", activeSubscriptions: 1 });
  });
});

describe("headline", () => {
  it("prefers the billing currency, else the largest amount, and counts the others", () => {
    const m = new Map([["TRY", BigInt(100)], ["EUR", BigInt(-900)]]);
    expect(headline(m, "TRY")).toEqual({ minor: "100", currency: "TRY", more: 1 });
    expect(headline(m)).toEqual({ minor: "-900", currency: "EUR", more: 1 });
    expect(headline(new Map())).toBeUndefined();
  });
});

describe("customer labels", () => {
  it("makes a flag from a country code and nothing from anything else", () => {
    expect(flagOf("tr")).toBe("🇹🇷");
    expect(flagOf("TUR")).toBe("");
    expect(flagOf(undefined)).toBe("");
  });

  it("names a country in the console's language", () => {
    expect(countryName("DE", "en")).toBe("Germany");
    expect(countryName("", "en")).toBe("");
  });

  it("takes initials from the name, the e-mail or the id", () => {
    expect(initialsOf({ name: "Ada Lovelace" })).toBe("AL");
    expect(initialsOf({ name: "  ", email: "grace.hopper@example.com" })).toBe("GH");
    expect(initialsOf({ external_id: "cus_1" })).toBe("C1");
    expect(initialsOf({ name: "ırmak", external_id: "x" }, "tr")).toBe("I");
    expect(initialsOf({})).toBe("?");
  });
});
