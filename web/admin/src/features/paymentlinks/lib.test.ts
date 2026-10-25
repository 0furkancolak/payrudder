import { describe, expect, it } from "vitest";
import { absoluteUrl, statusVariant, toRequest, type LinkForm } from "./lib";

const base: LinkForm = { kind: "adhoc", invoiceId: "", amount: "12.50", currency: "USD", description: "Setup", customer: "", promotion: "", singleUse: true, days: "30", key: "k1" };

describe("toRequest", () => {
  it("turns a typed amount into minor units", () => {
    expect(toRequest(base)).toEqual({ body: { kind: "adhoc", single_use: true, expires_in_days: 30, idempotency_key: "k1", amount_minor: "1250", currency: "USD", description: "Setup" } });
  });
  it("names the customer when given", () => {
    const r = toRequest({ ...base, customer: " cus_1 " });
    expect("body" in r && r.body.customer_external_id).toBe("cus_1");
  });
  it("carries the promotion code of an ad hoc link, and none when it is empty", () => {
    const r = toRequest({ ...base, promotion: " SPRING10 " });
    expect("body" in r && r.body.promotion_code).toBe("SPRING10");
    expect("body" in toRequest(base) && "promotion_code" in (toRequest(base) as { body: object }).body).toBe(false);
  });
  it("refuses a zero or malformed amount, a missing description and bad days", () => {
    expect(toRequest({ ...base, amount: "0" })).toEqual({ error: "amount" });
    expect(toRequest({ ...base, amount: "1.234" })).toEqual({ error: "amount" });
    expect(toRequest({ ...base, description: " " })).toEqual({ error: "description" });
    expect(toRequest({ ...base, days: "400" })).toEqual({ error: "days" });
  });
  it("needs an invoice for an invoice link", () => {
    expect(toRequest({ ...base, kind: "invoice" })).toEqual({ error: "invoice" });
    const r = toRequest({ ...base, kind: "invoice", invoiceId: "inv1", singleUse: false });
    expect(r).toEqual({ body: { kind: "invoice", single_use: false, expires_in_days: 30, idempotency_key: "k1", invoice_id: "inv1" } });
  });
});

describe("absoluteUrl", () => {
  it("keeps absolute links and anchors relative ones", () => {
    expect(absoluteUrl("https://a.example/i/x", "http://h")).toBe("https://a.example/i/x");
    expect(absoluteUrl("/i/x", "http://h")).toBe("http://h/i/x");
    expect(absoluteUrl(undefined, "http://h")).toBe("");
  });
});

describe("statusVariant", () => {
  it("maps statuses", () => {
    expect(statusVariant("paid")).toBe("success");
    expect(statusVariant("revoked")).toBe("destructive");
    expect(statusVariant("active")).toBe("secondary");
  });
});
