import type { components } from "@/shared/api/schema";

type S = components["schemas"];

/**
 * What "Create a sample meter and customer" makes in the sandbox, and the event "Send a test event" sends. The meter is the one the playground uses; the customer's id is
 * the one the code on the page starts with, so the first paste works as it stands. The payment method is the simulator's, which succeeds. Sandbox only: nothing here is
 * ever sent to a live tenant.
 */
export const SAMPLE_METER: S["CreateMeterRequest"] = { code: "api_calls", aggregation: "AGGREGATION_SUM", value_property: "n", quantity_scale: 0, unit_label: "calls" };
export const SAMPLE_CUSTOMER: S["CreateCustomerRequest"] = { external_id: "cus_1", name: "Sample customer", email: "sample.customer@example.com", payment_method: "pm_sandbox_success" };

/** One usage event of quantity 1 for a customer and meter, happening now. The key makes each press a new event (a sandbox does not require one, but the code a person ships does). */
export function testEvent(meter: string, customer: string, now: Date): S["IncomingEvent"] {
  return { customer_external_id: customer, meter_code: meter, quantity: "1", event_time: now.toISOString(), idempotency_key: `console-test-${now.getTime()}` };
}

/** Which of the two a tenant still lacks, given what the lists say: a sample is made only for what is missing, never on top of what exists. */
export function missingSamples(meters: readonly unknown[], customers: readonly unknown[]): { meter: boolean; customer: boolean } {
  return { meter: meters.length === 0, customer: customers.length === 0 };
}
