import { describe, expect, it } from "vitest";
import { LIVE_PROVIDERS, PROVIDER_FIELDS, buildProviderSettings, isLiveProvider, prefilled, problemCode } from "./liveProvider";

const iyzicoValues = (over: Record<string, string> = {}) => ({
  api_key: "api", secret_key: "secret", "buyer.name": "Ada", "buyer.surname": "Lovelace", "buyer.identity_number": "11111111111", "buyer.email": "ada@example.com",
  "buyer.address": "Bagdat Cd. 1", "buyer.city": "Istanbul", "buyer.country": "Turkey", "buyer.zip_code": "34000", "buyer.ip": "85.34.78.112", ...over,
});

describe("connecting a live provider yourself", () => {
  it("offers the providers Payrudder has adapters for, and never the mock", () => {
    expect([...LIVE_PROVIDERS]).toEqual(["iyzico", "paddle", "stripe", "banktransfer"]);
    expect(isLiveProvider("iyzico")).toBe(true);
    expect(isLiveProvider("mockpsp")).toBe(false);
    expect(isLiveProvider("mock-psp")).toBe(false);
  });

  it("never asks for the endpoint: a person gets the provider's own live host", () => {
    for (const fields of Object.values(PROVIDER_FIELDS)) expect(fields.map((f) => f.path)).not.toContain("base_url");
    const built = buildProviderSettings("iyzico", { ...iyzicoValues(), base_url: "https://evil.example" });
    expect(built.ok && "base_url" in built.settings).toBe(false);
  });

  it("builds the iyzico settings document with a nested buyer, leaving the optional phone out", () => {
    const r = buildProviderSettings("iyzico", iyzicoValues());
    expect(r).toEqual({
      ok: true,
      settings: {
        api_key: "api", secret_key: "secret",
        buyer: { name: "Ada", surname: "Lovelace", identity_number: "11111111111", email: "ada@example.com", address: "Bagdat Cd. 1", city: "Istanbul", country: "Turkey", zip_code: "34000", ip: "85.34.78.112" },
      },
    });
  });

  it("names every missing field, trimmed values only", () => {
    const r = buildProviderSettings("iyzico", iyzicoValues({ api_key: "  ", "buyer.city": "" }));
    expect(r).toEqual({ ok: false, errors: { api_key: "required", "buyer.city": "required" } });
  });

  it("checks the shape of the fields that have one", () => {
    expect(buildProviderSettings("iyzico", iyzicoValues({ "buyer.ip": "not an ip" }))).toEqual({ ok: false, errors: { "buyer.ip": "badFormat" } });
    const paddle = { api_key: "pdl_live_x", webhook_secret: "pdl_ntfset_x", usage_product_id: "pro_01abc" };
    expect(buildProviderSettings("paddle", paddle)).toEqual({ ok: true, settings: paddle });
    expect(buildProviderSettings("paddle", { ...paddle, usage_product_id: "01abc" })).toEqual({ ok: false, errors: { usage_product_id: "badFormat" } });
  });

  it("prefills the buyer from what is already known, and never a secret", () => {
    const v = prefilled("iyzico", { email: "ada@example.com", taxId: "123", line1: "Street 1", city: "Istanbul", country: "TR", postalCode: "34000" });
    expect(v["buyer.email"]).toBe("ada@example.com");
    expect(v["buyer.identity_number"]).toBe("123");
    expect(v.api_key).toBe("");
    expect(v.secret_key).toBe("");
    expect(Object.values(prefilled("paddle", { email: "x" })).every((x) => x === "")).toBe(true);
  });

  it("words only the problems it knows, and reads anything else as a refusal", () => {
    expect(problemCode(undefined)).toBeNull();
    expect(problemCode("")).toBeNull();
    expect(problemCode("unreadable")).toBe("unreadable");
    expect(problemCode("unreachable")).toBe("unreachable");
    expect(problemCode("refused")).toBe("refused");
    expect(problemCode("something new")).toBe("refused");
  });
});

describe("the Stripe form", () => {
  it("asks for a secret or restricted key and the webhook signing secret, and nothing else", () => {
    expect(PROVIDER_FIELDS.stripe.map((f) => f.path)).toEqual(["api_key", "webhook_secret"]);
    expect(PROVIDER_FIELDS.stripe.every((f) => f.secret)).toBe(true);
  });

  it("builds the settings payments-worker stores, and refuses what Stripe would refuse", () => {
    const ok = buildProviderSettings("stripe", { api_key: "sk_live_abc123", webhook_secret: "whsec_abc123" });
    expect(ok).toEqual({ ok: true, settings: { api_key: "sk_live_abc123", webhook_secret: "whsec_abc123" } });
    expect(buildProviderSettings("stripe", { api_key: "rk_test_abc", webhook_secret: "whsec_x" }).ok).toBe(true);
    const bad = buildProviderSettings("stripe", { api_key: "pk_live_publishable", webhook_secret: "secret" });
    expect(bad).toEqual({ ok: false, errors: { api_key: "badFormat", webhook_secret: "badFormat" } });
    expect(buildProviderSettings("stripe", { api_key: "", webhook_secret: "" }).ok).toBe(false);
  });
});
