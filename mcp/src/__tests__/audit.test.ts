import { describe, expect, it } from "vitest";
import { describeArgs, keyPrefix } from "../audit.js";

describe("what an audit line says of a call", () => {
  it("keeps scalars, cuts long text, counts lists and hides what is about a person", () => {
    const out = describeArgs({
      customer_external_id: "cus_42",
      page_size: 10,
      all: true,
      note: "x".repeat(200),
      status: "y".repeat(100),
      events: [1, 2, 3],
      properties: { a: 1 },
      name: "Ada Lovelace",
      email: "ada@example.com",
      reason: "chargeback",
    });
    expect(out).toEqual({
      customer_external_id: "cus_42",
      page_size: 10,
      all: true,
      note: "[not logged]",
      status: `${"y".repeat(80)}…`,
      events: { items: 3 },
      properties: "[not logged]",
      name: "[not logged]",
      email: "[not logged]",
      reason: "[not logged]",
    });
  });
});

describe("which key made a call", () => {
  it("is the public prefix, never the secret", () => {
    expect(keyPrefix("qk_test_ab12cd34_SECRETSECRETSECRET")).toBe("qk_test_ab12cd34");
    expect(keyPrefix("qk_live_zz99_x")).toBe("qk_live_zz99");
  });
  it("is empty for something that is not a key", () => {
    expect(keyPrefix(undefined)).toBe("");
    expect(keyPrefix("sk_live_whatever_secret")).toBe("");
    expect(keyPrefix("qk_test_nosecretpart")).toBe("");
  });
});
