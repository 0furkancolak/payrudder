import { describe, expect, it } from "vitest";
import { Payrudder } from "../src/index";

// Optional smoke test against a running stack: PAYRUDDER_API_KEY=qk_test_... [PAYRUDDER_BASE_URL=http://localhost:42800] npm test
describe.skipIf(!process.env.PAYRUDDER_API_KEY)("integration (live gateway)", () => {
  it("lists customers and meters with the configured key", async () => {
    const client = new Payrudder();
    const customers = await client.customers.listCustomers({ page_size: 1 });
    expect(Array.isArray(customers.customers ?? [])).toBe(true);
    const meters = await client.meters.listMeters();
    expect(meters).toBeTypeOf("object");
  });
});
