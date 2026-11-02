import { PayrudderError } from "@payrudder/sdk";
import { payrudder } from "../../lib/payrudder";

// POST /usage {"customer": "cus_42", "meter": "api_calls", "quantity": "1"} counts one event for that customer.
export async function POST(request: Request): Promise<Response> {
  const { customer, meter, quantity = "1" } = (await request.json().catch(() => null)) ?? {};
  if (typeof customer !== "string" || typeof meter !== "string") return Response.json({ error: "customer and meter are required" }, { status: 400 });
  try {
    // A key per request makes a retry of this call count once. A real app derives it from what happened (an order id), not from the clock.
    const key = request.headers.get("Idempotency-Key") ?? crypto.randomUUID();
    const [result] = await payrudder().events.ingest([{ customer_external_id: customer, meter_code: meter, quantity: String(quantity), event_time: new Date(), idempotency_key: key }]);
    return Response.json(result);
  } catch (err) {
    if (err instanceof PayrudderError) return Response.json({ error: err.key ?? err.message }, { status: 502 });
    throw err;
  }
}
