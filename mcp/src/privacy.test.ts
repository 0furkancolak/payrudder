import { describe, expect, it } from "vitest";
import { REDACTED, liveRefusal, redactPersonal } from "./privacy.js";

const keys = new Set(["email", "phone", "tax_id", "line1", "city"]);
const redact = (v: unknown) => redactPersonal(v, keys);

describe("redactPersonal", () => {
  it("replaces the personal keys of a customer, its name and its address, and leaves the rest", () => {
    const customer = { id: "c1", external_id: "cus_1", name: "Ada Lovelace", email: "ada@example.com", phone: "+905321234567", tax_id: "1234567890", country: "TR", locale: "tr", seats: 3, active: true, address: { line1: "1 Main St", city: "Ankara", postal_code: "06000", country: "TR" } };
    expect(redact(customer)).toEqual({
      count: 7,
      value: { ...customer, name: REDACTED, email: REDACTED, phone: REDACTED, tax_id: REDACTED, address: { line1: REDACTED, city: REDACTED, postal_code: REDACTED, country: "TR" } },
    });
  });

  it("finds customers in lists and in nested answers", () => {
    const out = redact({ customers: [{ external_id: "a", name: "A", email: "a@x.io" }, { external_id: "b", name: "B", email: "b@x.io" }], next_page_token: "t" }).value as { customers: Record<string, string>[]; next_page_token: string };
    expect(out.customers).toEqual([{ external_id: "a", name: REDACTED, email: REDACTED }, { external_id: "b", name: REDACTED, email: REDACTED }]);
    expect(out.next_page_token).toBe("t");
    expect((redact({ invoice: { customer: { external_id: "a", name: "A" } } }).value as any).invoice.customer.name).toBe(REDACTED);
  });

  it("keeps the seller's own names: a plan, a meter, a coupon and a tax rate", () => {
    const own = { plans: [{ code: "pro", name: "Pro" }], meters: [{ code: "api", name: "API calls" }], rates: [{ country: "DE", name: "VAT" }] };
    expect(redact(own)).toEqual({ value: own, count: 0 });
  });

  it("takes an object or a list under a personal key all the way down", () => {
    expect(redact({ email: ["a@x.io", { alt: "b@x.io" }, ""] })).toEqual({ value: { email: [REDACTED, { alt: REDACTED }, ""] }, count: 2 });
  });

  it("does not mark what is empty or not text, and redacts everything but the country under an address", () => {
    expect(redact({ email: "", phone: null, tax_id: 0, address: { district: "Çankaya", country: "TR", line2: "" } })).toEqual({ value: { email: "", phone: null, tax_id: 0, address: { district: REDACTED, country: "TR", line2: "" } }, count: 1 });
  });

  it("does not touch a value that holds no object", () => {
    expect(redact("a@x.io")).toEqual({ value: "a@x.io", count: 0 });
    expect(redact(null)).toEqual({ value: null, count: 0 });
  });
});

describe("liveRefusal", () => {
  it("refuses a live key unless live is allowed, and always accepts a sandbox key", () => {
    expect(liveRefusal("qk_live_ab12cd34_secret", false)).toContain("PAYRUDDER_MCP_ALLOW_LIVE=1");
    expect(liveRefusal("qk_live_ab12cd34_secret", true)).toBeUndefined();
    expect(liveRefusal("qk_test_ab12cd34_secret", false)).toBeUndefined();
  });
  it("never repeats the key", () => {
    expect(liveRefusal("qk_live_ab12cd34_TOPSECRET", false)).not.toContain("TOPSECRET");
  });
});
