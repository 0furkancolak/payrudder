import { describe, expect, it } from "vitest";
import { SAMPLE_CUSTOMER, SAMPLE_METER, missingSamples, testEvent } from "./sample";
import { snippets } from "./snippets";

describe("the sample made in the sandbox", () => {
  it("is the meter and the customer id the code on the page starts with", () => {
    const code = snippets("typescript", { baseUrl: "http://x", meter: SAMPLE_METER.code, customer: SAMPLE_CUSTOMER.external_id, keyHint: "qk_test_" });
    expect(code.send).toContain(`"${SAMPLE_CUSTOMER.external_id}"`);
    expect(SAMPLE_CUSTOMER.external_id).toBe("cus_1");
    expect(SAMPLE_CUSTOMER.payment_method).toMatch(/^pm_sandbox_/);
    expect(SAMPLE_METER).toMatchObject({ aggregation: "AGGREGATION_SUM", quantity_scale: 0 });
  });

  it("is made only for what is missing", () => {
    expect(missingSamples([], [])).toEqual({ meter: true, customer: true });
    expect(missingSamples([{}], [])).toEqual({ meter: false, customer: true });
    expect(missingSamples([], [{}])).toEqual({ meter: true, customer: false });
    expect(missingSamples([{}], [{}])).toEqual({ meter: false, customer: false });
  });
});

describe("the test event", () => {
  const now = new Date("2026-11-02T10:15:30.123Z");
  it("is one unit of the chosen meter for the chosen customer, happening now", () => {
    expect(testEvent("api_calls", "cus_9", now)).toEqual({
      customer_external_id: "cus_9",
      meter_code: "api_calls",
      quantity: "1",
      event_time: "2026-11-02T10:15:30.123Z",
      idempotency_key: `console-test-${now.getTime()}`,
    });
  });
  it("has a different key each time it is pressed, within the API's key pattern", () => {
    const a = testEvent("m", "c", now).idempotency_key;
    const b = testEvent("m", "c", new Date(now.getTime() + 1)).idempotency_key;
    expect(a).not.toBe(b);
    expect(a).toMatch(/^[A-Za-z0-9_.:-]{1,128}$/);
  });
});
